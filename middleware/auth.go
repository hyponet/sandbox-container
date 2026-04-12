package middleware

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// APIKeys holds the set of valid API keys loaded from the environment.
var apiKeys map[string]bool

// LoadAPIKeysFromEnv re-reads SANDBOX_API_KEY and updates the key set.
// Useful for testing or hot-reload scenarios.
func LoadAPIKeysFromEnv() {
	raw := os.Getenv("SANDBOX_API_KEY")
	apiKeys = make(map[string]bool)
	for _, k := range splitEnvKeys(raw) {
		apiKeys[k] = true
	}
}

// splitEnvKeys parses a comma-separated key string into individual trimmed keys.
func splitEnvKeys(raw string) []string {
	var keys []string
	for _, key := range strings.Split(raw, ",") {
		k := strings.TrimSpace(key)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func init() {
	apiKeys = make(map[string]bool)
	LoadAPIKeysFromEnv()
}

// AuthRequired returns a Gin middleware that validates the Bearer token in the
// Authorization header against the SANDBOX_API_KEY environment variable.
//
// If SANDBOX_API_KEY is not set, authentication is skipped (open mode).
// Multiple keys can be provided via comma-separated values.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// No keys configured — skip auth
		if len(apiKeys) == 0 {
			c.Next()
			return
		}

		token := extractBearerToken(c.GetHeader("Authorization"))
		if token == "" || !apiKeys[token] {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "unauthorized: invalid or missing API key",
			})
			return
		}

		c.Next()
	}
}

// extractBearerToken extracts the token from "Bearer <token>" format.
func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	// Also accept raw token without "Bearer" prefix
	return strings.TrimSpace(header)
}
