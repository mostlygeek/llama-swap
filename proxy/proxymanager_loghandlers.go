package proxy

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (pm *ProxyManager) sendLogsHandlers(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	history := pm.logMonitor.GetHistory()
	_, err := c.Writer.Write(history)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}
}

func (pm *ProxyManager) streamLogsHandler(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	c.Header("Transfer-Encoding", "chunked")
	c.Header("X-Content-Type-Options", "nosniff")

	ch := pm.logMonitor.Subscribe()
	defer pm.logMonitor.Unsubscribe(ch)

	notify := c.Request.Context().Done()
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("Streaming unsupported"))
		return
	}

	_, skipHistory := c.GetQuery("no-history")
	// Send history first if not skipped

	if !skipHistory {
		history := pm.logMonitor.GetHistory()
		if len(history) != 0 {
			_, err := c.Writer.Write(history)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			flusher.Flush()
		}
	}

	// Stream new logs
	for {
		select {
		case msg := <-ch:
			_, err := c.Writer.Write(msg)
			if err != nil {
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func (pm *ProxyManager) streamLogsHandlerSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")

	ch := pm.logMonitor.Subscribe()
	defer pm.logMonitor.Unsubscribe(ch)

	notify := c.Request.Context().Done()

	// Send history first if not skipped
	_, skipHistory := c.GetQuery("no-history")
	if !skipHistory {
		history := pm.logMonitor.GetHistory()
		if len(history) != 0 {
			c.SSEvent("message", string(history))
			c.Writer.Flush()
		}
	}

	// Stream new logs
	for {
		select {
		case msg := <-ch:
			c.SSEvent("message", string(msg))
			c.Writer.Flush()
		case <-notify:
			return
		}
	}
}
