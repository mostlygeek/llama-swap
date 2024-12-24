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
		// Set the Content-Type header to text/html
		c.Header("Content-Type", "text/html")

		// Write the embedded HTML content to the response
		logsHTML, err := getHTMLFile("logs.html")
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		_, err = c.Writer.Write(logsHTML)
		if err != nil {
			c.String(http.StatusInternalServerError, fmt.Sprintf("failed to write response: %v", err))
			return
		}
	} else {
		c.Header("Content-Type", "text/plain")
		history := pm.logMonitor.GetHistory()
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

	ch := pm.logMonitor.Subscribe()
	defer pm.logMonitor.Unsubscribe(ch)

	notify := c.Request.Context().Done()
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithError(http.StatusInternalServerError, fmt.Errorf("streaming unsupported"))
		return
	}

	_, skipHistory := c.GetQuery("no-history")
	// Send history first if not skipped

	if !skipHistory {
		history := pm.logMonitor.GetHistory()
		if len(history) != 0 {
			c.Writer.Write(history)
			flusher.Flush()
		}
	}

	// Stream new logs
	for {
		select {
		case msg := <-ch:
			_, err := c.Writer.Write(msg)
			if err != nil {
				// just break the loop if we can't write for some reason
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
