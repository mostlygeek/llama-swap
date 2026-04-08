package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type LazyConfigData struct {
	Proxy    string              `yaml:"proxy"`
	Commands map[string]string   `yaml:"commands"`
	Quant    map[string][]string `yaml:"quant"`
	Models   []string            `yaml:"models"`
}

type hfModelInfo struct {
	Siblings []struct {
		RFilename string `json:"rfilename"`
	} `json:"siblings"`
}

func getSpecificQuants(repoID string) ([]string, error) {
	apiURL := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model info for %s: %w", repoID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch model info for %s: status %d", repoID, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data hfModelInfo
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	quantPattern := regexp.MustCompile(`(?i)(I?Q\d_[A-Z0-9_]+|I?Q\d_[0-9]|F16|F32|BF16)`)
	fallbackPattern := regexp.MustCompile(`[\.\-_]`)

	foundQuants := make(map[string]bool)

	for _, sibling := range data.Siblings {
		file := sibling.RFilename
		if strings.HasSuffix(strings.ToLower(file), ".gguf") {
			matches := quantPattern.FindStringSubmatch(file)
			if len(matches) > 1 {
				foundQuants[strings.ToUpper(matches[1])] = true
			} else if strings.Contains(strings.ToUpper(file), "Q") {
				parts := fallbackPattern.Split(file, -1)
				for _, p := range parts {
					pu := strings.ToUpper(p)
					if strings.HasPrefix(pu, "Q") && regexp.MustCompile(`\d`).MatchString(pu) {
						foundQuants[pu] = true
					}
				}
			}
		}
	}

	var result []string
	for q := range foundQuants {
		result = append(result, q)
	}
	sort.Strings(result)
	return result, nil
}

func extendModel(model string, quantsNameList []string) []string {
	if len(quantsNameList) > 0 {
		var result []string
		for _, q := range quantsNameList {
			result = append(result, fmt.Sprintf("%s:%s", model, q))
		}
		return result
	}

	quants, err := getSpecificQuants(model)
	if err != nil || len(quants) == 0 {
		return []string{model}
	}

	var result []string
	for _, q := range quants {
		result = append(result, fmt.Sprintf("%s:%s", model, q))
	}
	return result
}

// LoadAndMergeLazyConfig reads a lazy config from disk, resolves models, and merges them into the provided Config.
func LoadAndMergeLazyConfig(mainConfig *Config, lazyConfigPath string) error {
	file, err := os.Open(lazyConfigPath)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	var lazy LazyConfigData
	if err := yaml.Unmarshal(data, &lazy); err != nil {
		return err
	}

	// Apply defaults
	if lazy.Proxy == "" {
		lazy.Proxy = "http://127.0.0.1:${PORT}"
	}
	if lazy.Commands == nil {
		lazy.Commands = make(map[string]string)
	}
	if lazy.Quant == nil {
		lazy.Quant = make(map[string][]string)
	}

	// Initialize Models map if it's nil
	if mainConfig.Models == nil {
		mainConfig.Models = make(map[string]ModelConfig)
	}

	type modelEntry struct {
		Name   string
		Config ModelConfig
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	results := make([]modelEntry, 0)

	// Semaphore to limit concurrency (e.g. 5 concurrent requests)
	sem := make(chan struct{}, 5)

	for _, model := range lazy.Models {
		wg.Add(1)
		go func(model string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Resolve model type and command
			var modelTypeStr string
			var commandStr string
			found := false

			for mType, cmd := range lazy.Commands {
				if strings.Contains(strings.ToLower(model), strings.ToLower(mType)) {
					modelTypeStr = mType
					commandStr = cmd
					found = true
					break
				}
			}

			if !found {
				commandStr = lazy.Commands["default"]
				if commandStr == "" {
					commandStr = lazy.Commands["defualt"] // Typo match from the python script
					if commandStr == "" {
						commandStr = "llama-server -hf ${model} --port ${PORT}"
					}
				}
				// Use empty string for lookup in quants if default fallback
				modelTypeStr = ""
			}

			var extendedModels []string
			if found {
				extendedModels = extendModel(model, lazy.Quant[modelTypeStr])
			} else {
				// No specific type found, try generic quants lookup or just use base model
				extendedModels = extendModel(model, lazy.Quant[""])
			}

			for _, mName := range extendedModels {
				cmdReplaced := strings.ReplaceAll(commandStr, "${model}", fmt.Sprintf("\"%s\"", mName))

				mc := ModelConfig{
					Proxy: lazy.Proxy,
					Cmd:   cmdReplaced,
				}

				mu.Lock()
				results = append(results, modelEntry{Name: mName, Config: mc})
				mu.Unlock()
			}
		}(model)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("encountered errors loading lazy config: %v", errs)
	}

	// Merge into main config Models
	for _, entry := range results {
		// Only add if it does not already exist,
		// allowing standard config.yaml to take precedence if both are used.
		if _, exists := mainConfig.Models[entry.Name]; !exists {
			mainConfig.Models[entry.Name] = entry.Config
		}
	}

	return nil
}
