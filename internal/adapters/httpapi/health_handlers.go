package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

func liveHandler(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "UP"}) }

func readyHandler(r *health.Readiness) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, results := r.Check(c.Request.Context())
		status, word := http.StatusOK, "UP"
		if !ok {
			status, word = http.StatusServiceUnavailable, "DOWN"
		}
		c.JSON(status, gin.H{"status": word, "checks": results})
	}
}
