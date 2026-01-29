import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useAPI } from "../contexts/APIProvider";
import { Panel, PanelGroup, PanelResizeHandle } from "react-resizable-panels";
import { useTheme } from "../contexts/ThemeProvider";
import Editor from "@monaco-editor/react";
import * as yaml from "js-yaml";
import { EXAMPLE_CONFIG, CLI_ARGS_REFERENCE } from "./ConfigConstants";

const Config = () => {
  const { fetchConfig, saveConfig } = useAPI();
  const { screenWidth, isDarkMode } = useTheme();
  const direction = screenWidth === "xs" || screenWidth === "sm" ? "vertical" : "horizontal";

  const [configContent, setConfigContent] = useState<string>("");
  const [configPath, setConfigPath] = useState<string>("");
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string>("");
  const [isSaving, setIsSaving] = useState(false);
  const [hasChanges, setHasChanges] = useState(false);
  const [validationError, setValidationError] = useState<string>("");
  const [cliArgsSearch, setCliArgsSearch] = useState<string>("");
  const [copiedArg, setCopiedArg] = useState<string>("");
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Load config on mount
  useEffect(() => {
    loadConfig();
  }, []);

  const loadConfig = useCallback(async () => {
    setIsLoading(true);
    setError("");
    try {
      const data = await fetchConfig();
      setConfigContent(data.content);
      setConfigPath(data.path);
      setHasChanges(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load config");
    } finally {
      setIsLoading(false);
    }
  }, [fetchConfig]);

  const handleEditorChange = useCallback((value: string | undefined) => {
    if (value !== undefined) {
      setConfigContent(value);
      setHasChanges(true);

      // Validate YAML on change
      try {
        yaml.load(value);
        setValidationError("");
      } catch (err) {
        setValidationError(err instanceof Error ? err.message : "Invalid YAML");
      }
    }
  }, []);

  const handleSave = useCallback(async () => {
    // Validate YAML before saving
    try {
      yaml.load(configContent);
    } catch (err) {
      alert(`Invalid YAML: ${err instanceof Error ? err.message : "Unknown error"}`);
      return;
    }

    // Show confirmation dialog
    if (!confirm("Are you sure you want to save the configuration? This will reload llama-swap.")) {
      return;
    }

    setIsSaving(true);
    setError("");
    try {
      await saveConfig(configContent);
      setHasChanges(false);
      alert("Configuration saved successfully! The server is reloading...");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save config");
      alert(`Failed to save: ${err instanceof Error ? err.message : "Unknown error"}`);
    } finally {
      setIsSaving(false);
    }
  }, [configContent, saveConfig]);

  const handleExport = useCallback(() => {
    const blob = new Blob([configContent], { type: "text/yaml" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "config.yaml";
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [configContent]);

  const handleImport = useCallback(() => {
    if (fileInputRef.current) {
      fileInputRef.current.click();
    }
  }, []);

  const handleFileSelect = useCallback((event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    const reader = new FileReader();
    reader.onload = (e) => {
      const content = e.target?.result as string;
      if (content) {
        setConfigContent(content);
        setHasChanges(true);
        // Validate the imported YAML
        try {
          yaml.load(content);
          setValidationError("");
        } catch (err) {
          setValidationError(err instanceof Error ? err.message : "Invalid YAML");
        }
      }
    };
    reader.readAsText(file);
    // Reset input so the same file can be selected again
    event.target.value = "";
  }, []);

  // Filter CLI args based on search
  const filteredCliArgs = useMemo(() => {
    if (!cliArgsSearch.trim()) return CLI_ARGS_REFERENCE;
    
    const searchLower = cliArgsSearch.toLowerCase();
    const lines = CLI_ARGS_REFERENCE.split('\n');
    const filteredLines: string[] = [];
    
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.toLowerCase().includes(searchLower)) {
        // Include some context: previous line and next line
        if (i > 0 && !filteredLines.includes(lines[i - 1])) {
          filteredLines.push(lines[i - 1]);
        }
        filteredLines.push(line);
        if (i < lines.length - 1 && !filteredLines.includes(lines[i + 1])) {
          filteredLines.push(lines[i + 1]);
        }
      }
    }
    
    return filteredLines.length > 0 ? filteredLines.join('\n') : 'No matches found.';
  }, [cliArgsSearch]);

  // Handle copying CLI argument to clipboard
  const handleCopyArg = useCallback(async (arg: string) => {
    try {
      await navigator.clipboard.writeText(arg);
      setCopiedArg(arg);
      setTimeout(() => setCopiedArg(""), 2000);
    } catch (err) {
      console.error("Failed to copy:", err);
    }
  }, []);

  // Helper function to highlight search terms
  const highlightSearch = useCallback((text: string, search: string, key: string): React.ReactNode => {
    if (!search || !text) return text;

    const lowerText = text.toLowerCase();
    const index = lowerText.indexOf(search);

    if (index === -1) return text;

    return (
      <span key={key}>
        {text.substring(0, index)}
        <mark className="bg-yellow-200 dark:bg-yellow-700">
          {text.substring(index, index + search.length)}
        </mark>
        {highlightSearch(text.substring(index + search.length), search, `${key}-next`)}
      </span>
    );
  }, []);

  // Render CLI args with clickable arguments and highlighted search results
  const renderCliArgs = useMemo(() => {
    const textToRender = cliArgsSearch.trim() ? filteredCliArgs : CLI_ARGS_REFERENCE;
    
    if (textToRender === 'No matches found.') {
      return <div className="text-gray-500 dark:text-gray-400">No matches found.</div>;
    }

    const lines = textToRender.split('\n');
    const searchLower = cliArgsSearch.toLowerCase();

    return lines.map((line, lineIndex) => {
      // Regex to match CLI arguments: --long-arg or -s
      const argRegex = /(-{1,2}[a-z0-9][-a-z0-9]*)/gi;
      const parts: React.ReactNode[] = [];
      let lastIndex = 0;
      let match;

      while ((match = argRegex.exec(line)) !== null) {
        const arg = match[1];
        const matchIndex = match.index;

        // Add text before the match
        if (matchIndex > lastIndex) {
          const textBefore = line.substring(lastIndex, matchIndex);
          parts.push(highlightSearch(textBefore, searchLower, `before-${lineIndex}-${lastIndex}`));
        }

        // Add the clickable argument
        parts.push(
          <button
            key={`arg-${lineIndex}-${matchIndex}`}
            onClick={() => handleCopyArg(arg)}
            className="text-blue-600 dark:text-blue-400 hover:underline hover:bg-blue-100 dark:hover:bg-blue-900/30 px-0.5 rounded cursor-pointer transition-colors"
            title={`Click to copy '${arg}'`}
          >
            {copiedArg === arg ? (
              <span className="bg-green-200 dark:bg-green-800 px-1 rounded">{arg}</span>
            ) : (
              highlightSearch(arg, searchLower, `arg-text-${lineIndex}-${matchIndex}`)
            )}
          </button>
        );

        lastIndex = matchIndex + arg.length;
      }

      // Add remaining text after last match
      if (lastIndex < line.length) {
        parts.push(highlightSearch(line.substring(lastIndex), searchLower, `after-${lineIndex}-${lastIndex}`));
      }

      // If no matches were found, just highlight the entire line
      if (parts.length === 0) {
        parts.push(highlightSearch(line, searchLower, `line-${lineIndex}`));
      }

      return (
        <div key={lineIndex}>
          {parts}
        </div>
      );
    });
  }, [cliArgsSearch, filteredCliArgs, copiedArg, handleCopyArg, highlightSearch]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-full">
        <p className="text-gray-600 dark:text-gray-400">Loading configuration...</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between mb-2 px-2">
        <div className="flex items-center gap-4">
          <h2 className="text-lg font-semibold">Configuration Editor</h2>
          <span className="text-sm text-gray-600 dark:text-gray-400">{configPath}</span>
          {hasChanges && <span className="text-sm text-orange-600 dark:text-orange-400">● Unsaved changes</span>}
        </div>
        <div className="flex gap-2">
          <input
            type="file"
            ref={fileInputRef}
            onChange={handleFileSelect}
            accept=".yaml,.yml"
            className="hidden"
          />
          <button
            onClick={handleImport}
            className="px-3 py-2 bg-gray-600 text-white rounded hover:bg-gray-700 text-sm"
            title="Import config file"
          >
            Import Config
          </button>
          <button
            onClick={handleExport}
            className="px-3 py-2 bg-gray-600 text-white rounded hover:bg-gray-700 text-sm"
            title="Export current config"
          >
            Export Config
          </button>
          <button
            onClick={handleSave}
            disabled={isSaving || !hasChanges || !!validationError}
            className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700 disabled:bg-gray-400 disabled:cursor-not-allowed"
          >
            {isSaving ? "Saving..." : "Save Config"}
          </button>
        </div>
      </div>

      {error && (
        <div className="mb-2 px-2 py-2 bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400 rounded">
          Error: {error}
        </div>
      )}

      {validationError && (
        <div className="mb-2 px-2 py-2 bg-orange-100 dark:bg-orange-900/30 text-orange-700 dark:text-orange-400 rounded text-sm">
          YAML Validation: {validationError}
        </div>
      )}

      <PanelGroup direction={direction} className="flex-1 gap-2" autoSaveId="config-panel-group">
        <Panel id="editor" defaultSize={50} minSize={20} maxSize={80}>
          <div className="h-full border border-border rounded overflow-hidden">
            <Editor
              height="100%"
              defaultLanguage="yaml"
              value={configContent}
              onChange={handleEditorChange}
              theme={isDarkMode ? "vs-dark" : "vs-light"}
              options={{
                minimap: { enabled: true },
                scrollBeyondLastLine: false,
                fontSize: 14,
                wordWrap: "on",
                automaticLayout: true,
              }}
            />
          </div>
        </Panel>

        <PanelResizeHandle
          className={
            direction === "horizontal"
              ? "w-2 h-full bg-primary hover:bg-success transition-colors rounded"
              : "w-full h-2 bg-primary hover:bg-success transition-colors rounded"
          }
        />

        <Panel id="reference" defaultSize={50} minSize={20} maxSize={80}>
          <PanelGroup direction="vertical" className="gap-2">
            <Panel id="example" defaultSize={60} minSize={20}>
              <div className="h-full border border-border rounded flex flex-col">
                <div className="px-3 py-2 bg-gray-100 dark:bg-gray-800 border-b border-border">
                  <h3 className="text-sm font-semibold">config.example.yaml</h3>
                </div>
                <div className="flex-1 overflow-hidden">
                  <Editor
                    height="100%"
                    defaultLanguage="yaml"
                    value={EXAMPLE_CONFIG}
                    theme={isDarkMode ? "vs-dark" : "vs-light"}
                    options={{
                      readOnly: true,
                      minimap: { enabled: false },
                      scrollBeyondLastLine: false,
                      fontSize: 12,
                      wordWrap: "on",
                      automaticLayout: true,
                      lineNumbers: "on",
                    }}
                  />
                </div>
              </div>
            </Panel>

            <PanelResizeHandle className="w-full h-2 bg-primary hover:bg-success transition-colors rounded" />

            <Panel id="cli-args" defaultSize={40} minSize={20}>
              <div className="h-full border border-border rounded flex flex-col">
                <div className="px-3 py-2 bg-gray-100 dark:bg-gray-800 border-b border-border space-y-2">
                  <div className="flex items-center justify-between">
                    <h3 className="text-sm font-semibold">llama-server CLI Arguments</h3>
                    <span className="text-xs text-gray-500 dark:text-gray-400 italic">Click args to copy</span>
                  </div>
                  <input
                    type="text"
                    placeholder="Search arguments..."
                    value={cliArgsSearch}
                    onChange={(e) => setCliArgsSearch(e.target.value)}
                    className="w-full px-2 py-1 text-xs border border-border rounded bg-background text-foreground focus:outline-none focus:ring-1 focus:ring-blue-500"
                  />
                </div>
                <div className="flex-1 overflow-auto p-4">
                  <div className="text-xs whitespace-pre-wrap font-mono text-foreground">
                    {renderCliArgs}
                  </div>
                </div>
              </div>
            </Panel>
          </PanelGroup>
        </Panel>
      </PanelGroup>
    </div>
  );
};

export default Config;
