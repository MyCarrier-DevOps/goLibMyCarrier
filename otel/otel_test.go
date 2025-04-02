package otel

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestInitZapLogger(t *testing.T) {
	logger, err := InitZapLogger()
	assert.NoError(t, err)
	assert.NotNil(t, logger)
}

func TestZapLoggerForGin(t *testing.T) {
	logger, _ := InitZapLogger()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ZapLoggerForGin(logger))
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "test")
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "test", w.Body.String())
}

func TestZapLoggerForGin_PanicRecovery(t *testing.T) {
	logger, _ := InitZapLogger()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ZapLoggerForGin(logger))
	router.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	req, _ := http.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	defer func() {
		if r := recover(); r != nil {
			t.Log("Recovered from panic:", r)
		}
	}()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestZapLoggerForGin_CustomPath(t *testing.T) {
	logger, _ := InitZapLogger()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ZapLoggerForGin(logger))
	router.GET("/custom/path", func(c *gin.Context) {
		c.String(http.StatusOK, "custom path")
	})

	req, _ := http.NewRequest("GET", "/custom/path", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "custom path", w.Body.String())
}
