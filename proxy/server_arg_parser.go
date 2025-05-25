package proxy

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ServerArgs holds information parsed or inferred from server command line arguments.
type ServerArgs struct {
	Architecture      string
	ContextLength     int
	Capabilities      []string
	Family            string
	ParameterSize     string
	QuantizationLevel string
	CmdAlias          string
	CmdModelPath      string // Base name of the model file from --model arg
}

// ServerArgParser defines an interface for parsing server command line arguments.
type ServerArgParser interface {
	Parse(cmdStr string, modelID string) ServerArgs
}

// LlamaServerParser implements ServerArgParser for llama-server.
type LlamaServerParser struct{}

var (
	architecturePatterns = map[string]*regexp.Regexp{
		"command-r": regexp.MustCompile(`(?i)command-r`),
		"gemma2":    regexp.MustCompile(`(?i)gemma2`),
		"gemma3":    regexp.MustCompile(`(?i)gemma3`),
		"gemma":     regexp.MustCompile(`(?i)gemma`),
		"llama4":    regexp.MustCompile(`(?i)llama-?4`),
		"llama3":    regexp.MustCompile(`(?i)llama-?3`),
		"llama":     regexp.MustCompile(`(?i)llama`),
		"mistral3":  regexp.MustCompile(`(?i)mistral-?3`),
		"mistral":   regexp.MustCompile(`(?i)mistral`),
		"phi3":      regexp.MustCompile(`(?i)phi-?3`),
		"phi":       regexp.MustCompile(`(?i)phi`),
		"qwen2.5vl": regexp.MustCompile(`(?i)qwen-?2\.5-?vl`),
		"qwen3":     regexp.MustCompile(`(?i)qwen-?3`),
		"qwen2":     regexp.MustCompile(`(?i)qwen-?2`),
		"qwen":      regexp.MustCompile(`(?i)qwen`),
		"bert":      regexp.MustCompile(`(?i)bert`),
		"clip":      regexp.MustCompile(`(?i)clip`),
	}
	orderedArchKeys = []string{
		"command-r", "gemma3", "gemma2", "gemma", "llama4", "llama3", "llama",
		"mistral3", "mistral", "phi3", "phi", "qwen2.5vl", "qwen3", "qwen2", "qwen",
		"bert", "clip",
	}

	parameterSizePattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?(?:x\d+)?)[BMGT]?B`)
	quantizationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)IQ[1-4]_(XXS|XS|S|M|NL)`),
		regexp.MustCompile(`(?i)Q[2-8]_(0|1|[KSLM]+(?:_[KSLM]+)?)`),
		regexp.MustCompile(`(?i)BPW\d+`),
		regexp.MustCompile(`(?i)GGML_TYPE_Q[2-8]_\d`),
		regexp.MustCompile(`(?i)F(?:P)?(16|32)`),
		regexp.MustCompile(`(?i)BF16`),
	}
)

func inferPattern(name string, patterns map[string]*regexp.Regexp, orderedKeys []string) string {
	nameLower := strings.ToLower(name)
	for _, key := range orderedKeys {
		pattern, ok := patterns[key]
		if !ok || pattern == nil {
			continue
		}
		if pattern.MatchString(nameLower) {
			return key
		}
	}
	return "unknown"
}

func inferQuantizationLevelFromName(name string) string {
	for _, pattern := range quantizationPatterns {
		match := pattern.FindString(name)
		if match != "" {
			return strings.ToUpper(match)
		}
	}
	return "unknown"
}

func inferParameterSizeFromName(name string) string {
	match := parameterSizePattern.FindStringSubmatch(name)
	if len(match) > 0 {
		return strings.ToUpper(match[0])
	}
	return "unknown"
}

func inferFamilyFromName(nameForInference string, currentArch string) string {
	if currentArch != "unknown" && currentArch != "" {
		re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)`)
		match := re.FindStringSubmatch(currentArch)
		if len(match) > 1 {
			potentialFamily := strings.ToLower(match[1])
			knownFamilies := []string{"llama", "qwen", "phi", "mistral", "gemma", "command-r", "bert", "clip"}
			for _, kf := range knownFamilies {
				if potentialFamily == kf {
					return kf
				}
			}
			for _, kf := range knownFamilies {
				if strings.ToLower(currentArch) == kf {
					return kf
				}
			}
		}
	}
	orderedFamilyCheckKeys := []string{"command-r", "gemma", "llama", "mistral", "phi", "qwen", "bert", "clip"}
	familyPatterns := make(map[string]*regexp.Regexp)
	for _, key := range orderedFamilyCheckKeys {
		if p, ok := architecturePatterns[key]; ok {
			familyPatterns[key] = p
		}
	}
	return inferPattern(nameForInference, familyPatterns, orderedFamilyCheckKeys)
}

// NewLlamaServerParser creates a new parser for llama-server arguments.
func NewLlamaServerParser() *LlamaServerParser {
	return &LlamaServerParser{}
}

// Parse extracts relevant information from llama-server command string and modelID.
func (p *LlamaServerParser) Parse(cmdStr string, modelID string) ServerArgs {
	parsed := ServerArgs{
		Capabilities: []string{"completion"}, // Default
	}

	args, err := SanitizeCommand(cmdStr)
	if err != nil {
		// If sanitization fails, proceed with inference based on modelID only
		parsed.Architecture = inferPattern(modelID, architecturePatterns, orderedArchKeys)
		parsed.Family = inferFamilyFromName(modelID, parsed.Architecture)
		parsed.ParameterSize = inferParameterSizeFromName(modelID)
		parsed.QuantizationLevel = inferQuantizationLevelFromName(modelID)
		return parsed
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-c", "--ctx-size":
			if i+1 < len(args) {
				if valInt, err := strconv.Atoi(args[i+1]); err == nil {
					parsed.ContextLength = valInt
				}
				i++
			}
		case "-a", "--alias":
			if i+1 < len(args) {
				parsed.CmdAlias = args[i+1]
				i++
			}
		case "-m", "--model":
			if i+1 < len(args) {
				parsed.CmdModelPath = filepath.Base(args[i+1])
				i++
			}
		case "--jinja":
			foundTools := false
			for _, cap := range parsed.Capabilities {
				if cap == "tools" {
					foundTools = true
					break
				}
			}
			if !foundTools {
				parsed.Capabilities = append(parsed.Capabilities, "tools")
			}
		}
	}

	parsed.Architecture = inferPattern(modelID, architecturePatterns, orderedArchKeys)
	if parsed.Architecture == "unknown" {
		parsed.Architecture = inferPattern(parsed.CmdAlias, architecturePatterns, orderedArchKeys)
	}
	if parsed.Architecture == "unknown" {
		parsed.Architecture = inferPattern(parsed.CmdModelPath, architecturePatterns, orderedArchKeys)
	}

	parsed.Family = inferFamilyFromName(modelID, parsed.Architecture)
	if parsed.Family == "unknown" {
		parsed.Family = inferFamilyFromName(parsed.CmdAlias, parsed.Architecture)
	}
	if parsed.Family == "unknown" {
		parsed.Family = inferFamilyFromName(parsed.CmdModelPath, parsed.Architecture)
	}

	parsed.ParameterSize = inferParameterSizeFromName(modelID)
	if parsed.ParameterSize == "unknown" {
		parsed.ParameterSize = inferParameterSizeFromName(parsed.CmdAlias)
	}
	if parsed.ParameterSize == "unknown" {
		parsed.ParameterSize = inferParameterSizeFromName(parsed.CmdModelPath)
	}

	parsed.QuantizationLevel = inferQuantizationLevelFromName(modelID)
	if parsed.QuantizationLevel == "unknown" {
		parsed.QuantizationLevel = inferQuantizationLevelFromName(parsed.CmdAlias)
	}
	if parsed.QuantizationLevel == "unknown" {
		parsed.QuantizationLevel = inferQuantizationLevelFromName(parsed.CmdModelPath)
	}

	return parsed
}
