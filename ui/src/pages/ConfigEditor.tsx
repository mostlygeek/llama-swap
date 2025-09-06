import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { RiAlignJustify, RiFontSize, RiTextWrap } from "react-icons/ri";
import { usePersistentState } from "../hooks/usePersistentState";
import { useAPI } from "../contexts/APIProvider";

type FontSize = "xxs" | "xs" | "small" | "normal";

const ConfigEditor = () => {
  const { connectionStatus } = useAPI();

  // Editor state
  const [content, setContent] = useState("");
  const [originalContent, setOriginalContent] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [successMsg, setSuccessMsg] = useState<string | null>(null);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // UI prefs (persisted like LogPanel)
  const [wrapText, setWrapText] = usePersistentState("configEditor-wrapText", false);
  const [fontSize, setFontSize] = usePersistentState<FontSize>("configEditor-fontSize", "normal");

  const isDirty = useMemo(() => content !== originalContent, [content, originalContent]);

  // abort controller for fetches
  const loadAbortRef = useRef<AbortController | null>(null);
  const prevConnRef = useRef(connectionStatus);

  const fontSizeClass = useMemo(() => {
    switch (fontSize) {
      case "xxs":
        return "text-[0.5rem]";
      case "xs":
        return "text-[0.75rem]";
      case "small":
        return "text-[0.875rem]";
      case "normal":
        return "text-base";
    }
    return "text-base";
  }, [fontSize]);

  const toggleFontSize = useCallback(() => {
    setFontSize((prev) => {
      switch (prev) {
        case "xxs":
          return "xs";
        case "xs":
          return "small";
        case "small":
          return "normal";
        case "normal":
          return "xxs";
      }
    });
  }, [setFontSize]);

  const toggleWrap = useCallback(() => setWrapText((prev) => !prev), [setWrapText]);

  const readErrorResponse = useCallback(async (res: Response) => {
    try {
      const ct = res.headers.get("content-type") || "";
      if (ct.includes("application/json")) {
        const data = await res.json();
        if (data && typeof data.error === "string") {
          return data.error as string;
        }
        return JSON.stringify(data);
      }
      const txt = await res.text();
      return txt || `Request failed with status ${res.status}`;
    } catch (e: unknown) {
      return `Request failed with status ${res.status}`;
    }
  }, []);

  const fetchConfig = useCallback(async () => {
    setLoading(true);
    setErrorMsg(null);
    setSuccessMsg(null);

    loadAbortRef.current?.abort();
    const controller = new AbortController();
    loadAbortRef.current = controller;

    try {
      const res = await fetch("/api/config", {
        method: "GET",
        signal: controller.signal,
      });
      if (!res.ok) {
        const msg = await readErrorResponse(res);
        throw new Error(msg);
      }
      const text = await res.text();
      setContent(text);
      setOriginalContent(text);
    } catch (e: any) {
      if (e?.name === "AbortError") {
        // ignore aborts
      } else {
        setErrorMsg(e?.message || "Failed to load config");
      }
    } finally {
      if (!controller.signal.aborted) {
        setLoading(false);
      }
    }
  }, [readErrorResponse]);

  // Initial load
  useEffect(() => {
    fetchConfig();
    return () => {
      loadAbortRef.current?.abort();
    };
  }, [fetchConfig]);

  // Re-fetch when reconnects and editor is clean
  useEffect(() => {
    const prev = prevConnRef.current;
    if (prev !== "connected" && connectionStatus === "connected" && !isDirty) {
      fetchConfig();
    }
    prevConnRef.current = connectionStatus;
  }, [connectionStatus, isDirty, fetchConfig]);

  const handleSave = useCallback(async () => {
    if (!isDirty || saving) return;
    setSaving(true);
    setErrorMsg(null);
    setSuccessMsg(null);

    try {
      const payload = content; // capture exactly what we send
      const res = await fetch("/api/config", {
        method: "PUT",
        headers: {
          "Content-Type": "text/plain",
        },
        body: payload,
      });

      if (!res.ok) {
        const msg = await readErrorResponse(res);
        throw new Error(msg);
      }

      // success: mark exactly what was persisted
      setOriginalContent(payload);
      setSuccessMsg("Saved");
      // transient success
      setTimeout(() => setSuccessMsg(null), 1500);
    } catch (e: any) {
      setErrorMsg(e?.message || "Failed to save config");
    } finally {
      setSaving(false);
    }
  }, [content, isDirty, saving, readErrorResponse]);

  return (
    <div className="card h-full flex flex-col">
      <div className="shrink-0">
        <div className="flex items-center justify-between">
          <h2>Config</h2>

          <div className="flex items-center gap-2">
            <button className="btn" onClick={toggleFontSize} title="Font size">
              <RiFontSize />
            </button>
            <button className="btn" onClick={toggleWrap} title={wrapText ? "Disable wrap" : "Enable wrap"}>
              {wrapText ? <RiTextWrap /> : <RiAlignJustify />}
            </button>
            <button
              className="btn"
              onClick={handleSave}
              disabled={!isDirty || saving || loading}
              title="Save configuration"
            >
              {saving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>

        <div className="mt-2 min-h-[24px]">
          {loading && <span className="text-txtsecondary">Loading...</span>}
          {!loading && successMsg && <span className="text-success">{successMsg}</span>}
          {!loading && errorMsg && <span className="text-error">{errorMsg}</span>}
          {!loading && !successMsg && !errorMsg && (
            <span className="text-txtsecondary">{isDirty ? "Unsaved changes" : "Up to date"}</span>
          )}
        </div>
      </div>

      <div className="flex-1 min-h-0 mt-2">
        <textarea
          className={`w-full h-full font-mono ${fontSizeClass} bg-background text-foreground border border-border rounded p-3 outline-none focus:border-primary focus:ring-0 resize-none`}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          spellCheck={false}
          wrap={wrapText ? "soft" : "off"}
          disabled={loading}
          aria-label="YAML configuration editor"
        />
      </div>
    </div>
  );
};

export default ConfigEditor;