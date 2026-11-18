package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func write(c *gin.Context, status int, body any) {
	if status == http.StatusNoContent {
		c.Status(status)
		return
	}
	c.JSON(status, body)
}
