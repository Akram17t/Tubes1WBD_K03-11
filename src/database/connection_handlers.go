package database

import (
	"errors"
	"net/http"
	"strconv"

	"tubes-wbd/src/auth"

	"github.com/gin-gonic/gin"
)

type connectionHandler struct{ service *ConnectionService }

// RegisterAdminRoutes mounts connection administration separately from viewer routes.
func RegisterAdminRoutes(router gin.IRouter, service *ConnectionService) {
	h := connectionHandler{service: service}
	r := router.Group("/admin/databases", auth.RequireLogin(), auth.RequireAdmin(), auth.CSRF())
	r.GET("", h.list)
	r.GET("/new", h.newForm)
	r.POST("", h.create)
	r.GET("/:dbID/edit", h.edit)
	r.POST("/:dbID/update", h.update)
	r.POST("/:dbID/test", h.test)
	r.POST("/:dbID/activate", h.activate)
	r.POST("/:dbID/deactivate", h.deactivate)
}

func (h connectionHandler) list(c *gin.Context) {
	user, _ := auth.CurrentUser(c)
	connections, err := h.service.List(c.Request.Context(), user)
	if err != nil {
		connectionError(c, err)
		return
	}
	c.HTML(http.StatusOK, "connections.html", gin.H{"Connections": connections, "CSRFToken": auth.CSRFToken(c)})
}

func (h connectionHandler) newForm(c *gin.Context) {
	renderConnectionForm(c, http.StatusOK, Connection{Port: 5432, SSLMode: "require"}, "")
}

func (h connectionHandler) create(c *gin.Context) {
	input := connectionFormInput(c)
	user, _ := auth.CurrentUser(c)
	if err := h.service.Create(c.Request.Context(), user, input); err != nil {
		if errors.Is(err, ErrInvalidConnection) || errors.Is(err, ErrConnectionNameExists) || errors.Is(err, ErrConnectionTargetExists) {
			renderConnectionForm(c, connectionStatus(err), safeFormConnection(0, input), err.Error())
		} else {
			connectionError(c, err)
		}
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/databases")
}

func (h connectionHandler) edit(c *gin.Context) {
	connection, ok := h.find(c)
	if !ok {
		return
	}
	renderConnectionForm(c, http.StatusOK, connection, "")
}

func (h connectionHandler) update(c *gin.Context) {
	connection, ok := h.find(c)
	if !ok {
		return
	}
	input := connectionFormInput(c)
	user, _ := auth.CurrentUser(c)
	if err := h.service.Update(c.Request.Context(), user, connection.ID, input); err != nil {
		if errors.Is(err, ErrInvalidConnection) || errors.Is(err, ErrConnectionNameExists) || errors.Is(err, ErrConnectionTargetExists) {
			form := safeFormConnection(connection.ID, input)
			form.IsActive = connection.IsActive
			renderConnectionForm(c, connectionStatus(err), form, err.Error())
		} else {
			connectionError(c, err)
		}
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/databases/"+strconv.FormatInt(connection.ID, 10)+"/edit")
}

func (h connectionHandler) test(c *gin.Context) {
	connection, ok := h.find(c)
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.Test(c.Request.Context(), user, connection.ID); err != nil {
		message := "Pengujian koneksi gagal."
		if errors.Is(err, ErrTargetUnavailable) || errors.Is(err, ErrTargetTimeout) || errors.Is(err, ErrConnectionInactive) {
			message = err.Error()
		}
		renderConnectionForm(c, connectionStatus(err), connection, message)
		return
	}
	// The query contains only a result marker, never connection credentials.
	c.Redirect(http.StatusSeeOther, "/admin/databases/"+strconv.FormatInt(connection.ID, 10)+"/edit?test=ok")
}

func (h connectionHandler) activate(c *gin.Context)   { h.setActive(c, true) }
func (h connectionHandler) deactivate(c *gin.Context) { h.setActive(c, false) }

func (h connectionHandler) setActive(c *gin.Context, active bool) {
	id, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
	if err != nil || id <= 0 {
		connectionError(c, ErrInvalidConnection)
		return
	}
	user, _ := auth.CurrentUser(c)
	if err := h.service.SetActive(c.Request.Context(), user, id, active); err != nil {
		connectionError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin/databases")
}

func (h connectionHandler) find(c *gin.Context) (Connection, bool) {
	id, err := strconv.ParseInt(c.Param("dbID"), 10, 64)
	if err != nil || id <= 0 {
		connectionError(c, ErrInvalidConnection)
		return Connection{}, false
	}
	user, _ := auth.CurrentUser(c)
	connection, err := h.service.Find(c.Request.Context(), user, id)
	if err != nil {
		connectionError(c, err)
		return Connection{}, false
	}
	return connection, true
}

func connectionFormInput(c *gin.Context) ConnectionInput {
	port, _ := strconv.Atoi(c.PostForm("port"))
	return ConnectionInput{Name: c.PostForm("name"), Host: c.PostForm("host"), Port: port, DatabaseName: c.PostForm("database_name"), DBUsername: c.PostForm("db_username"), SSLMode: c.PostForm("ssl_mode"), Password: c.PostForm("password")}
}

func safeFormConnection(id int64, input ConnectionInput) Connection {
	return Connection{ID: id, Name: input.Name, Host: input.Host, Port: input.Port, DatabaseName: input.DatabaseName, DBUsername: input.DBUsername, SSLMode: input.SSLMode}
}

func renderConnectionForm(c *gin.Context, status int, connection Connection, message string) {
	c.HTML(status, "connection_form.html", gin.H{"Connection": connection, "Error": message, "TestOK": c.Query("test") == "ok", "CSRFToken": auth.CSRFToken(c), "SSLModes": []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}})
}

func connectionStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalidConnection):
		return http.StatusBadRequest
	case errors.Is(err, ErrConnectionNotFound):
		return http.StatusNotFound
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrConnectionNameExists), errors.Is(err, ErrConnectionTargetExists), errors.Is(err, ErrConnectionInactive):
		return http.StatusConflict
	case errors.Is(err, ErrTargetTimeout):
		return http.StatusGatewayTimeout
	case errors.Is(err, ErrTargetUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func connectionError(c *gin.Context, err error) {
	status := connectionStatus(err)
	message := "Operasi koneksi tidak dapat diproses."
	if status != http.StatusInternalServerError {
		message = err.Error()
	}
	c.String(status, "%s", message)
}
