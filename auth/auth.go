package auth

import (
	"encoding/base64"
	"log"
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
			log.Println("AuthMiddleware instance is nil")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			c.Abort()
			return
		}

		// Check if validApiKey is set
		if am.validApiKey == "" {
			log.Println("validApiKey is not set")
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
			log.Printf("Failed to decode Base64 string: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}
		// log.Printf("Decoded API Key: %s", decodedApiKey)

		// Convert decoded API key to string
		decodedApiKeyStr := string(decodedApiKey)

		// Validate the API key
		if decodedApiKeyStr != am.validApiKey {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// If validation is successful, proceed to the next handler
		c.Next()
	}
}
