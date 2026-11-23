package database

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"tubes-wbd/src/api"
	"tubes-wbd/src/auth"
	"tubes-wbd/src/httpapi"

	"github.com/gin-gonic/gin"
)

type connectionChanges struct {
	Name         json.RawMessage `json:"name"`
	Host         json.RawMessage `json:"host"`
	Port         json.RawMessage `json:"port"`
	DatabaseName json.RawMessage `json:"database_name"`
	DBUsername   json.RawMessage `json:"db_username"`
	SSLMode      json.RawMessage `json:"ssl_mode"`
	Password     json.RawMessage `json:"password"`
	IsActive     json.RawMessage `json:"is_active"`
}

func registerConnectionAPIRoutes(router gin.IRouter, service *ConnectionService) {
	h := connectionHandler{service: service}
	r := router.Group("/api/admin/connections", auth.RequireLogin(), auth.RequireAdmin(), auth.CSRF())
	r.GET("", h.listAPI)
	r.GET("/:database_id", h.getAPI)
	r.POST("", h.createAPI)
	r.PATCH("/:database_id", h.patchAPI)
	r.POST("/:database_id/test", h.testAPI)
}

// Field yang tidak dikirim tetap memakai nilai lama; null ditolak karena tidak ada kolom nullable.
func (changes connectionChanges) apply(current Connection) (ConnectionInput, bool, error) {
	input := ConnectionInput{Name: current.Name, Host: current.Host, Port: current.Port,
		DatabaseName: current.DatabaseName, DBUsername: current.DBUsername, SSLMode: current.SSLMode}
	active := current.IsActive
	for _, field := range []struct {
		raw    json.RawMessage
		target any
	}{
		{changes.Name, &input.Name}, {changes.Host, &input.Host}, {changes.Port, &input.Port},
		{changes.DatabaseName, &input.DatabaseName}, {changes.DBUsername, &input.DBUsername},
		{changes.SSLMode, &input.SSLMode}, {changes.Password, &input.Password}, {changes.IsActive, &active},
	} {
		if field.raw != nil && (bytes.Equal(bytes.TrimSpace(field.raw), []byte("null")) || json.Unmarshal(field.raw, field.target) != nil) {
			return ConnectionInput{}, false, httpapi.ErrInvalidInput
		}
	}
	return input, active, nil
}

func (h connectionHandler) listAPI(c *gin.Context) {
	page, ok := api.ParsePage(c)
	if !ok {
		return
	}
	connections, err := (connectionRepository{db: h.service.platformDB}).listPage(c.Request.Context(), page.Limit+1, page.Offset)
	if err != nil {
		databaseAPIError(c, err)
		return
	}
	hasMore := len(connections) > page.Limit
	if hasMore {
		connections = connections[:page.Limit]
	}
	api.List(c, connections, page, hasMore)
}

func (h connectionHandler) getAPI(c *gin.Context) {
	id, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	connection, err := h.service.Find(c.Request.Context(), user, id)
	databaseAPIRespond(c, http.StatusOK, connection, err)
}

func (h connectionHandler) createAPI(c *gin.Context) {
	var changes connectionChanges
	if err := httpapi.Decode(c, &changes); err != nil {
		databaseAPIError(c, err)
		return
	}
	input, active, err := changes.apply(Connection{Port: 5432, SSLMode: "require", IsActive: true})
	if err != nil {
		databaseAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	connection, err := h.service.create(c.Request.Context(), user, input, active)
	databaseAPIRespond(c, http.StatusCreated, connection, err)
}

func (h connectionHandler) patchAPI(c *gin.Context) {
	id, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	var changes connectionChanges
	if err := httpapi.Decode(c, &changes); err != nil {
		databaseAPIError(c, err)
		return
	}
	user, _ := auth.CurrentUser(c)
	connection, err := h.service.patch(c.Request.Context(), user, id, changes)
	databaseAPIRespond(c, http.StatusOK, connection, err)
}

// Lock mencegah PATCH bersamaan menimpa field yang tidak dikirim request ini.
func (s *ConnectionService) patch(ctx context.Context, user *auth.User, id int64, changes connectionChanges) (Connection, error) {
	if err := auth.Authorize(ctx, s.platformDB, user, auth.ManagePlatform, 0); err != nil {
		return Connection{}, err
	}
	tx, err := s.platformDB.BeginTx(ctx, nil)
	if err != nil {
		return Connection{}, err
	}
	defer tx.Rollback()
	repository := connectionRepository{db: tx}
	previous, err := repository.lockActive(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	current, err := repository.find(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	input, active, err := changes.apply(current)
	if err != nil {
		return Connection{}, err
	}
	input, err = validateConnection(input, false)
	if err != nil {
		return Connection{}, err
	}
	var encrypted string
	if input.Password != "" {
		encrypted, err = s.encryptPassword(input.Password)
		if err != nil {
			return Connection{}, err
		}
	}
	if err := repository.update(ctx, id, input, encrypted); err != nil {
		return Connection{}, err
	}
	if previous != active {
		if err := repository.setActive(ctx, id, active); err != nil {
			return Connection{}, err
		}
		if !active {
			ids, err := repository.affectedUserIDs(ctx, id)
			if err != nil {
				return Connection{}, err
			}
			if err := auth.RevokeSessions(ctx, tx, ids); err != nil {
				return Connection{}, err
			}
		}
	}
	connection, err := repository.find(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	return connection, s.commitConfiguration(tx, id)
}

func (h connectionHandler) testAPI(c *gin.Context) {
	id, ok := api.PathID(c, "database_id")
	if !ok {
		return
	}
	user, _ := auth.CurrentUser(c)
	err := h.service.Test(c.Request.Context(), user, id)
	databaseAPIRespond(c, http.StatusOK, gin.H{"success": true}, err)
}
