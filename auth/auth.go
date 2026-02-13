package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware represents the authentication middleware
type AuthMiddleware struct {
	validApiKey string
}

// NewAuthMiddleware creates a new AuthMiddleware instance
func NewAuthMiddleware(apiKey string) *AuthMiddleware {
	return &AuthMiddleware{
		validApiKey: apiKey,
	}
}

// MiddlewareFunc creates a Gin middleware function for API key validation
func (am *AuthMiddleware) MiddlewareFunc() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if AuthMiddleware instance is nil
		if am == nil {
			slog.Error("AuthMiddleware instance is nil")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			c.Abort()
			return
		}

		// Check if validApiKey is set
		if am.validApiKey == "" {
			slog.Error("validApiKey is not set")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			c.Abort()
			return
		}

		// Get the Authorization header
		authHeader := c.GetHeader("Authorization")

		// Check if the Authorization header is present and properly formatted
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// Extract the API key from the Authorization header
		apiKey := strings.TrimPrefix(authHeader, "Bearer ")

		// Decode the Base64 ApiKey
		decodedApiKey, err := base64.StdEncoding.DecodeString(apiKey)
		if err != nil {
			slog.Error("failed to decode Base64 API key", "error", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// Convert decoded API key to string
		decodedApiKeyStr := string(decodedApiKey)

		// Validate the API key using constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(decodedApiKeyStr), []byte(am.validApiKey)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// If validation is successful, proceed to the next handler
		c.Next()
	}
}
