package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// User adalah user login yang dititipkan middleware session ke context Gin
type User struct {
	ID              int64
	Username        string
	DisplayName     string
	IsPlatformAdmin bool
}

const userKey = "auth.user"

// buat nitipin user login ke context, dipanggil middleware session
func setCurrentUser(c *gin.Context, user *User) {
	c.Set(userKey, user)
}

// ambil user yang lagi login, ok false kalau belum login
func CurrentUser(c *gin.Context) (*User, bool) {
	value, exists := c.Get(userKey)
	if !exists {
		return nil, false
	}

	user, ok := value.(*User)
	if !ok || user == nil {
		return nil, false
	}
	return user, true
}

// tendang ke /login kalau belum login
func RequireLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := CurrentUser(c); !ok {
			c.Redirect(http.StatusSeeOther, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

// cuma Platform Admin yang boleh lewat, pasang setelah RequireLogin
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || !user.IsPlatformAdmin {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
