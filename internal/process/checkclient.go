package process

import (
	"net"
	"net/http"
	"time"
)

var checkclient = &http.Client{
	// wait a short time for a tcp connection to be established
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 500 * time.Millisecond,
		}).DialContext,
	},

	// give a long time to respond to the health check endpoint
	// after the connection is established. See issue: 276
	Timeout: 5000 * time.Millisecond,
}
