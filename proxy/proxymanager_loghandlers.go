package proxy

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (pm *ProxyManager) sendLogsHandlers(c *gin.Context) {
	accept := c.GetHeader("Accept")
	if strings.Contains(accept, "text/html") {
		c.Redirect(http.StatusFound, "/ui/")
	} else {
		c.Header("Content-Type", "text/plain")
		history := pm.muxLogger.GetHistory()
		_, err := c.Writer.Write(history)
		if err != nil {
			c.AbortWithError(http.StatusInternalServerError, err)
			return
		}
	}
}

func (pm *ProxyManager) streamLogsHandler(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Content-Type-Options", "nosniff")

	logMonitorId := c.Param("logMonitorID")
	logger, err := pm.getLogger(logMonitorId)
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	_, skipHistory := c.GetQuery("no-history")
	// Send history first if not skipped

	if !skipHistory {
		history := logger.GetHistory()
		if len(history) != 0 {
			c.Writer.Write(history)
			flusher.Flush()
		}
	}

	defer logger.OnLogData(func(data []byte) {
		if c != nil && c.Writer != nil {
			c.Writer.Write(data)
			flusher.Flush()
		}
	})()

	select {
	case <-c.Request.Context().Done():
	case <-pm.shutdownCtx.Done():
	}

}

func (pm *ProxyManager) streamLogsHandlerSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")

	logMonitorId := c.Param("logMonitorID")
	logger, err := pm.getLogger(logMonitorId)
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}

	// Send history first if not skipped
	_, skipHistory := c.GetQuery("no-history")
	if !skipHistory {
		history := logger.GetHistory()
		if len(history) != 0 {
			c.SSEvent("message", string(history))
			c.Writer.Flush()
		}
	}

	defer logger.OnLogData(func(data []byte) {
		if c != nil && c.Writer != nil {
			c.SSEvent("message", string(data))
			c.Writer.Flush()
		}
	})()

	select {
	case <-c.Request.Context().Done():
	case <-pm.shutdownCtx.Done():
	}
}

// getLogger searches for the appropriate logger based on the logMonitorId
func (pm *ProxyManager) getLogger(logMonitorId string) (*LogMonitor, error) {
	var logger *LogMonitor

	if logMonitorId == "" {
		// maintain the default
		logger = pm.muxLogger
	} else if logMonitorId == "proxy" {
		logger = pm.proxyLogger
	} else if logMonitorId == "upstream" {
		logger = pm.upstreamLogger
	} else {
		return nil, fmt.Errorf("invalid logger. Use 'proxy' or 'upstream'")
	}

	return logger, nil
}
