package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mostlygeek/llama-swap/proxy/config"
	"gopkg.in/yaml.v3"
)

const (
	recipesBackendDirEnv       = "LLAMA_SWAP_RECIPES_BACKEND_DIR"
	defaultRecipesBackendDir   = "/home/csolutions_ai/spark-vllm-docker"
	defaultRecipeGroupName     = "managed-recipes"
	recipeMetadataKey          = "recipe_ui"
	recipeMetadataManagedField = "managed"
)

var (
	recipeRunnerRe = regexp.MustCompile(`(?:^|\s)(?:exec\s+)?(?:\$\{recipe_runner\}|[^\s'"]*run-recipe\.sh)\s+([^\s'"]+)`)
	recipeTpRe     = regexp.MustCompile(`(?:^|\s)--tp\s+([0-9]+)`)
	recipeNodesRe  = regexp.MustCompile(`(?:^|\s)-n\s+("?[^"\s]+"?|\$\{[^}]+\}|[^\s]+)`)
)

type recipeCatalogMeta struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Model       string         `yaml:"model"`
	SoloOnly    bool           `yaml:"solo_only"`
	ClusterOnly bool           `yaml:"cluster_only"`
	Defaults    map[string]any `yaml:"defaults"`
}

type RecipeCatalogItem struct {
	ID                    string `json:"id"`
	Ref                   string `json:"ref"`
	Path                  string `json:"path"`
	Name                  string `json:"name"`
	Description           string `json:"description"`
	Model                 string `json:"model"`
	SoloOnly              bool   `json:"soloOnly"`
	ClusterOnly           bool   `json:"clusterOnly"`
	DefaultTensorParallel int    `json:"defaultTensorParallel"`
}

type RecipeManagedModel struct {
	ModelID               string   `json:"modelId"`
	RecipeRef             string   `json:"recipeRef"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Aliases               []string `json:"aliases"`
	UseModelName          string   `json:"useModelName"`
	Mode                  string   `json:"mode"` // solo|cluster
	TensorParallel        int      `json:"tensorParallel"`
	Nodes                 string   `json:"nodes,omitempty"`
	ExtraArgs             string   `json:"extraArgs,omitempty"`
	Group                 string   `json:"group"`
	Unlisted              bool     `json:"unlisted,omitempty"`
	Managed               bool     `json:"managed"`
	BenchyTrustRemoteCode *bool    `json:"benchyTrustRemoteCode,omitempty"`
}

type RecipeUIState struct {
	ConfigPath string               `json:"configPath"`
	BackendDir string               `json:"backendDir"`
	Recipes    []RecipeCatalogItem  `json:"recipes"`
	Models     []RecipeManagedModel `json:"models"`
	Groups     []string             `json:"groups"`
}

type upsertRecipeModelRequest struct {
	ModelID               string   `json:"modelId"`
	RecipeRef             string   `json:"recipeRef"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Aliases               []string `json:"aliases"`
	UseModelName          string   `json:"useModelName"`
	Mode                  string   `json:"mode"` // solo|cluster
	TensorParallel        int      `json:"tensorParallel"`
	Nodes                 string   `json:"nodes,omitempty"`
	ExtraArgs             string   `json:"extraArgs,omitempty"`
	Group                 string   `json:"group"`
	Unlisted              bool     `json:"unlisted,omitempty"`
	BenchyTrustRemoteCode *bool    `json:"benchyTrustRemoteCode,omitempty"`
}

func (pm *ProxyManager) apiGetRecipeState(c *gin.Context) {
	state, err := pm.buildRecipeUIState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (pm *ProxyManager) apiUpsertRecipeModel(c *gin.Context) {
	var req upsertRecipeModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	state, err := pm.upsertRecipeModel(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (pm *ProxyManager) apiDeleteRecipeModel(c *gin.Context) {
	modelID := strings.TrimSpace(c.Param("id"))
	if modelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model id is required"})
		return
	}

	state, err := pm.deleteRecipeModel(modelID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, state)
}

func (pm *ProxyManager) buildRecipeUIState() (RecipeUIState, error) {
	configPath, err := pm.getConfigPath()
	if err != nil {
		return RecipeUIState{}, err
	}

	catalog, _, err := loadRecipeCatalog(recipesBackendDir())
	if err != nil {
		return RecipeUIState{}, err
	}

	root, err := loadConfigRawMap(configPath)
	if err != nil {
		return RecipeUIState{}, err
	}

	modelsMap := getMap(root, "models")
	groupsMap := getMap(root, "groups")

	models := make([]RecipeManagedModel, 0, len(modelsMap))
	for modelID, raw := range modelsMap {
		modelMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rm, ok := toRecipeManagedModel(modelID, modelMap, groupsMap)
		if ok {
			models = append(models, rm)
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ModelID < models[j].ModelID })

	groupNames := sortedGroupNames(groupsMap)
	return RecipeUIState{
		ConfigPath: configPath,
		BackendDir: recipesBackendDir(),
		Recipes:    catalog,
		Models:     models,
		Groups:     groupNames,
	}, nil
}

func (pm *ProxyManager) upsertRecipeModel(req upsertRecipeModelRequest) (RecipeUIState, error) {
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		return RecipeUIState{}, errors.New("modelId is required")
	}
	recipeRefInput := strings.TrimSpace(req.RecipeRef)
	if recipeRefInput == "" {
		return RecipeUIState{}, errors.New("recipeRef is required")
	}

	configPath, err := pm.getConfigPath()
	if err != nil {
		return RecipeUIState{}, err
	}

	catalog, catalogByID, err := loadRecipeCatalog(recipesBackendDir())
	if err != nil {
		return RecipeUIState{}, err
	}
	_ = catalog

	resolvedRecipeRef, catalogRecipe, err := resolveRecipeRef(recipeRefInput, catalogByID)
	if err != nil {
		return RecipeUIState{}, err
	}

	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		if catalogRecipe.SoloOnly {
			mode = "solo"
		} else {
			mode = "cluster"
		}
	}
	if mode != "solo" && mode != "cluster" {
		return RecipeUIState{}, errors.New("mode must be 'solo' or 'cluster'")
	}
	if catalogRecipe.SoloOnly && mode != "solo" {
		return RecipeUIState{}, fmt.Errorf("recipe %s requires solo mode", recipeRefInput)
	}
	if catalogRecipe.ClusterOnly && mode != "cluster" {
		return RecipeUIState{}, fmt.Errorf("recipe %s requires cluster mode", recipeRefInput)
	}

	tp := req.TensorParallel
	if tp <= 0 {
		tp = catalogRecipe.DefaultTensorParallel
	}
	if tp <= 0 {
		tp = 1
	}

	root, err := loadConfigRawMap(configPath)
	if err != nil {
		return RecipeUIState{}, err
	}
	modelsMap := getMap(root, "models")
	groupsMap := getMap(root, "groups")

	nodes := strings.TrimSpace(req.Nodes)
	if mode == "cluster" && nodes == "" {
		if hasMacro(root, "vllm_nodes") {
			nodes = "${vllm_nodes}"
		} else {
			return RecipeUIState{}, errors.New("nodes is required for cluster mode (macro vllm_nodes not found)")
		}
	}

	groupName := strings.TrimSpace(req.Group)
	if groupName == "" {
		groupName = defaultRecipeGroupName
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = modelID
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = catalogRecipe.Description
	}

	useModelName := strings.TrimSpace(req.UseModelName)
	if useModelName == "" {
		useModelName = catalogRecipe.Model
	}

	existing := getMap(modelsMap, modelID)
	modelEntry := cloneMap(existing)
	modelEntry["name"] = name
	modelEntry["description"] = description
	modelEntry["proxy"] = "http://127.0.0.1:${PORT}"
	modelEntry["checkEndpoint"] = "/health"
	modelEntry["ttl"] = 0
	modelEntry["useModelName"] = useModelName
	modelEntry["unlisted"] = req.Unlisted
	modelEntry["aliases"] = cleanAliases(req.Aliases)

	cmdStopExpr := "true"
	stopPrefix := ""
	if hasMacro(root, "vllm_stop_cluster") {
		cmdStopExpr = "${vllm_stop_cluster}"
		stopPrefix = "${vllm_stop_cluster}; "
	}

	runner := filepath.Join(recipesBackendDir(), "run-recipe.sh")
	if hasMacro(root, "recipe_runner") {
		runner = "${recipe_runner}"
	}

	var cmdParts []string
	cmdParts = append(cmdParts, "bash -lc '", stopPrefix, "exec ", runner, " ", quoteForCommand(resolvedRecipeRef))
	if mode == "solo" {
		cmdParts = append(cmdParts, " --solo")
	} else {
		cmdParts = append(cmdParts, " -n ", quoteForCommand(nodes))
	}
	if tp > 0 {
		cmdParts = append(cmdParts, " --tp ", strconv.Itoa(tp))
	}
	cmdParts = append(cmdParts, " --port ${PORT}")
	if extra := strings.TrimSpace(req.ExtraArgs); extra != "" {
		cmdParts = append(cmdParts, " ", extra)
	}
	cmdParts = append(cmdParts, "'")

	modelEntry["cmd"] = strings.Join(cmdParts, "")
	modelEntry["cmdStop"] = fmt.Sprintf("bash -lc '%s'", cmdStopExpr)

	meta := getMap(existing, "metadata")
	if len(meta) == 0 {
		meta = map[string]any{}
	}
	meta[recipeMetadataKey] = map[string]any{
		recipeMetadataManagedField: true,
		"recipe_ref":               resolvedRecipeRef,
		"mode":                     mode,
		"tensor_parallel":          tp,
		"nodes":                    nodes,
		"extra_args":               strings.TrimSpace(req.ExtraArgs),
		"group":                    groupName,
		"backend_dir":              recipesBackendDir(),
	}
	if req.BenchyTrustRemoteCode != nil {
		benchyMeta := getMap(meta, "benchy")
		benchyMeta["trust_remote_code"] = *req.BenchyTrustRemoteCode
		meta["benchy"] = benchyMeta
	}
	modelEntry["metadata"] = meta

	modelsMap[modelID] = modelEntry
	root["models"] = modelsMap

	removeModelFromAllGroups(groupsMap, modelID)
	group := getMap(groupsMap, groupName)
	if _, ok := group["swap"]; !ok {
		group["swap"] = true
	}
	if _, ok := group["exclusive"]; !ok {
		group["exclusive"] = true
	}
	members := append(groupMembers(group), modelID)
	group["members"] = uniqueStrings(members)
	groupsMap[groupName] = group
	root["groups"] = groupsMap

	if err := writeConfigRawMap(configPath, root); err != nil {
		return RecipeUIState{}, err
	}

	if conf, err := config.LoadConfig(configPath); err == nil {
		pm.Lock()
		pm.config = conf
		pm.Unlock()
	}
	return pm.buildRecipeUIState()
}

func (pm *ProxyManager) deleteRecipeModel(modelID string) (RecipeUIState, error) {
	configPath, err := pm.getConfigPath()
	if err != nil {
		return RecipeUIState{}, err
	}

	root, err := loadConfigRawMap(configPath)
	if err != nil {
		return RecipeUIState{}, err
	}

	modelsMap := getMap(root, "models")
	if _, ok := modelsMap[modelID]; !ok {
		return RecipeUIState{}, fmt.Errorf("model %s not found", modelID)
	}
	delete(modelsMap, modelID)
	root["models"] = modelsMap

	groupsMap := getMap(root, "groups")
	removeModelFromAllGroups(groupsMap, modelID)
	root["groups"] = groupsMap

	if err := writeConfigRawMap(configPath, root); err != nil {
		return RecipeUIState{}, err
	}

	if conf, err := config.LoadConfig(configPath); err == nil {
		pm.Lock()
		pm.config = conf
		pm.Unlock()
	}
	return pm.buildRecipeUIState()
}

func recipesBackendDir() string {
	if v := strings.TrimSpace(os.Getenv(recipesBackendDirEnv)); v != "" {
		return v
	}
	return defaultRecipesBackendDir
}

func (pm *ProxyManager) getConfigPath() (string, error) {
	if v := strings.TrimSpace(pm.configPath); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("LLAMA_SWAP_CONFIG_PATH")); v != "" {
		return v, nil
	}
	return "", errors.New("config path is unknown (start llama-swap with --config)")
}

func loadRecipeCatalog(backendDir string) ([]RecipeCatalogItem, map[string]RecipeCatalogItem, error) {
	recipesDir := filepath.Join(strings.TrimSpace(backendDir), "recipes")
	items := make([]RecipeCatalogItem, 0, 8)
	byID := make(map[string]RecipeCatalogItem)

	err := filepath.WalkDir(recipesDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		var meta recipeCatalogMeta
		if err := yaml.Unmarshal(raw, &meta); err != nil {
			return nil // skip malformed recipe files instead of failing whole API
		}

		base := filepath.Base(path)
		id := strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
		defaultTP := intFromAny(meta.Defaults["tensor_parallel"])
		if defaultTP <= 0 {
			defaultTP = 1
		}

		item := RecipeCatalogItem{
			ID:                    id,
			Ref:                   id,
			Path:                  path,
			Name:                  strings.TrimSpace(meta.Name),
			Description:           strings.TrimSpace(meta.Description),
			Model:                 strings.TrimSpace(meta.Model),
			SoloOnly:              meta.SoloOnly,
			ClusterOnly:           meta.ClusterOnly,
			DefaultTensorParallel: defaultTP,
		}
		items = append(items, item)
		byID[id] = item
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}

	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, byID, nil
}

func resolveRecipeRef(recipeRef string, catalogByID map[string]RecipeCatalogItem) (string, RecipeCatalogItem, error) {
	if item, ok := catalogByID[recipeRef]; ok {
		return item.Ref, item, nil
	}

	candidates := []string{
		recipeRef,
		filepath.Join(recipesBackendDir(), "recipes", recipeRef),
		filepath.Join(recipesBackendDir(), "recipes", recipeRef+".yaml"),
		filepath.Join(recipesBackendDir(), "recipes", recipeRef+".yml"),
		filepath.Join("/home/csolutions_ai/llama-swap/recipes", recipeRef),
		filepath.Join("/home/csolutions_ai/llama-swap/recipes", recipeRef+".yaml"),
		filepath.Join("/home/csolutions_ai/llama-swap/recipes", recipeRef+".yml"),
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if stat, err := os.Stat(c); err == nil && !stat.IsDir() {
			item := RecipeCatalogItem{
				ID:   filepath.Base(strings.TrimSuffix(strings.TrimSuffix(c, ".yaml"), ".yml")),
				Ref:  c,
				Path: c,
				Name: filepath.Base(c),
			}
			return c, item, nil
		}
	}
	return "", RecipeCatalogItem{}, fmt.Errorf("recipeRef not found: %s", recipeRef)
}

func loadConfigRawMap(configPath string) (map[string]any, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	normalized := normalizeYAMLValue(parsed)
	root, ok := normalized.(map[string]any)
	if !ok || root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

func writeConfigRawMap(configPath string, root map[string]any) error {
	rendered, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	if _, err := config.LoadConfigFromReader(bytes.NewReader(rendered)); err != nil {
		return fmt.Errorf("generated config is invalid: %w", err)
	}

	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, rendered, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}

func normalizeYAMLValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = normalizeYAMLValue(vv)
		}
		return m
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[fmt.Sprintf("%v", k)] = normalizeYAMLValue(vv)
		}
		return m
	case []any:
		out := make([]any, 0, len(t))
		for _, item := range t {
			out = append(out, normalizeYAMLValue(item))
		}
		return out
	default:
		return v
	}
}

func getMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return map[string]any{}
	}
	if key == "" {
		return parent
	}
	if raw, ok := parent[key]; ok {
		if m, ok := raw.(map[string]any); ok {
			return m
		}
	}
	m := map[string]any{}
	parent[key] = m
	return m
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if raw, ok := m[key]; ok {
		return strings.TrimSpace(fmt.Sprintf("%v", raw))
	}
	return ""
}

func getBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	b, parsed := parseAnyBool(v)
	return parsed && b
}

func hasMacro(root map[string]any, name string) bool {
	macros := getMap(root, "macros")
	_, ok := macros[name]
	return ok
}

func groupMembers(group map[string]any) []string {
	raw, ok := group["members"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s := strings.TrimSpace(fmt.Sprintf("%v", item))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func removeModelFromAllGroups(groups map[string]any, modelID string) {
	for groupName, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		members := groupMembers(group)
		filtered := make([]string, 0, len(members))
		for _, m := range members {
			if m != modelID {
				filtered = append(filtered, m)
			}
		}
		group["members"] = toAnySlice(filtered)
		groups[groupName] = group
	}
}

func sortedGroupNames(groups map[string]any) []string {
	names := make([]string, 0, len(groups))
	for groupName := range groups {
		names = append(names, groupName)
	}
	sort.Strings(names)
	return names
}

func toAnySlice(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func uniqueStrings(items []string) []any {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		s := strings.TrimSpace(item)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return toAnySlice(out)
}

func cleanAliases(aliases []string) []string {
	seen := make(map[string]struct{}, len(aliases))
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		s := strings.TrimSpace(alias)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func toRecipeManagedModel(modelID string, modelMap, groupsMap map[string]any) (RecipeManagedModel, bool) {
	cmd := getString(modelMap, "cmd")
	metadata := getMap(modelMap, "metadata")
	recipeMeta := getMap(metadata, recipeMetadataKey)
	managed := getBool(recipeMeta, recipeMetadataManagedField)

	if !managed && !strings.Contains(cmd, "run-recipe") {
		return RecipeManagedModel{}, false
	}

	recipeRef := getString(recipeMeta, "recipe_ref")
	if recipeRef == "" && cmd != "" {
		if m := recipeRunnerRe.FindStringSubmatch(cmd); len(m) > 1 {
			recipeRef = strings.TrimSpace(m[1])
		}
	}

	mode := getString(recipeMeta, "mode")
	if mode == "" {
		if strings.Contains(cmd, "--solo") {
			mode = "solo"
		} else {
			mode = "cluster"
		}
	}

	tp := intFromAny(recipeMeta["tensor_parallel"])
	if tp <= 0 && cmd != "" {
		if m := recipeTpRe.FindStringSubmatch(cmd); len(m) > 1 {
			tp, _ = strconv.Atoi(m[1])
		}
	}
	if tp <= 0 {
		tp = 1
	}

	nodes := getString(recipeMeta, "nodes")
	if nodes == "" && cmd != "" {
		if m := recipeNodesRe.FindStringSubmatch(cmd); len(m) > 1 {
			nodes = strings.Trim(m[1], `"`)
		}
	}

	groupName := getString(recipeMeta, "group")
	if groupName == "" {
		groupName = findModelGroup(modelID, groupsMap)
	}
	if groupName == "" {
		groupName = defaultRecipeGroupName
	}

	aliases := make([]string, 0)
	if rawAliases, ok := modelMap["aliases"].([]any); ok {
		for _, a := range rawAliases {
			s := strings.TrimSpace(fmt.Sprintf("%v", a))
			if s != "" {
				aliases = append(aliases, s)
			}
		}
	}

	var benchyTrustRemoteCode *bool
	if benchy := getMap(metadata, "benchy"); len(benchy) > 0 {
		if v, ok := benchy["trust_remote_code"]; ok {
			if parsed, ok := parseAnyBool(v); ok {
				benchyTrustRemoteCode = &parsed
			}
		}
	}

	return RecipeManagedModel{
		ModelID:               modelID,
		RecipeRef:             recipeRef,
		Name:                  getString(modelMap, "name"),
		Description:           getString(modelMap, "description"),
		Aliases:               aliases,
		UseModelName:          getString(modelMap, "useModelName"),
		Mode:                  mode,
		TensorParallel:        tp,
		Nodes:                 nodes,
		ExtraArgs:             getString(recipeMeta, "extra_args"),
		Group:                 groupName,
		Unlisted:              getBool(modelMap, "unlisted"),
		Managed:               managed,
		BenchyTrustRemoteCode: benchyTrustRemoteCode,
	}, true
}

func findModelGroup(modelID string, groups map[string]any) string {
	for groupName, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, member := range groupMembers(group) {
			if member == modelID {
				return groupName
			}
		}
	}
	return ""
}

func intFromAny(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(t))
		return i
	default:
		return 0
	}
}

func quoteForCommand(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return strconv.Quote(s)
	}
	return s
}
