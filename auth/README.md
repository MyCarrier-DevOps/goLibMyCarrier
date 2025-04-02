# Authentication Middleware Library

This library provides a middleware utility for API key-based authentication in a Gin web framework. It includes functionality for validating API keys and ensuring secure access to protected routes.

## Features

- Validate API keys using a `Bearer` token in the `Authorization` header.
- Decode Base64-encoded API keys for added security.
- Integrate seamlessly with the Gin web framework.

## Usage

### Creating an AuthMiddleware Instance

Use the `NewAuthMiddleware` function to create a new instance of the middleware:

```go
authMiddleware := NewAuthMiddleware("your-valid-api-key")
```

### Applying the Middleware

Apply the middleware to your Gin routes:

```go
r := gin.Default()

authMiddleware := NewAuthMiddleware("your-valid-api-key")

protected := r.Group("/protected")
protected.Use(authMiddleware.MiddlewareFunc())
protected.GET("/example", func(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"message": "Access granted"})
})

r.Run()
```

### Authorization Header Format

The client must include the API key in the `Authorization` header using the `Bearer` scheme. The API key should be Base64-encoded:

```
Authorization: Bearer <Base64-encoded-API-key>
```

### Error Handling

The middleware handles the following scenarios:

- Missing or improperly formatted `Authorization` header.
- Invalid or incorrectly decoded API key.
- Internal server errors if the middleware is not properly configured.

## Example

Here is an example of a request with a valid API key:

```bash
curl -H "Authorization: Bearer <Base64-encoded-API-key>" http://localhost:8080/protected/example
```

If the API key is valid, the response will be:

```json
{
    "message": "Access granted"
}
```

## Dependencies

This library uses the following dependencies:

- [Gin Web Framework](https://github.com/gin-gonic/gin): HTTP web framework for Go.
