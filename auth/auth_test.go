package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_ValidApiKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validApiKey := "test-api-key"
	encodedApiKey := base64.StdEncoding.EncodeToString([]byte(validApiKey))
	authMiddleware := NewAuthMiddleware(validApiKey)

	router := gin.New()
	router.Use(authMiddleware.MiddlewareFunc())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+encodedApiKey)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "success")
}

func TestAuthMiddleware_InvalidApiKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validApiKey := "test-api-key"
	authMiddleware := NewAuthMiddleware(validApiKey)

	router := gin.New()
	router.Use(authMiddleware.MiddlewareFunc())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-api-key")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
	assert.Contains(t, resp.Body.String(), "Unauthorized")
}

func TestAuthMiddleware_MissingAuthorizationHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validApiKey := "test-api-key"
	authMiddleware := NewAuthMiddleware(validApiKey)

	router := gin.New()
	router.Use(authMiddleware.MiddlewareFunc())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
	assert.Contains(t, resp.Body.String(), "Unauthorized")
}

func TestAuthMiddleware_InvalidBase64ApiKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validApiKey := "test-api-key"
	authMiddleware := NewAuthMiddleware(validApiKey)

	router := gin.New()
	router.Use(authMiddleware.MiddlewareFunc())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid-base64")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
	assert.Contains(t, resp.Body.String(), "Unauthorized")
}

func TestAuthMiddleware_NilMiddlewareInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var authMiddleware *AuthMiddleware

	router := gin.New()
	router.Use(authMiddleware.MiddlewareFunc())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Contains(t, resp.Body.String(), "Internal server error")
}

func TestAuthMiddleware_EmptyValidApiKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	authMiddleware := NewAuthMiddleware("")

	router := gin.New()
	router.Use(authMiddleware.MiddlewareFunc())
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest(http.MethodGet, "/test", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Contains(t, resp.Body.String(), "Internal server error")
}
