package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"tubes-wbd/src/auth"
	"tubes-wbd/src/migration"
	testdb "tubes-wbd/test/database"
)

func TestWorkflowApprovalBeforeExecute(t *testing.T) {
	db := testdb.Open(t)
	testdb.Exec(t, db, `
		INSERT INTO users (id,username,password_hash,display_name)
		VALUES (1,'editor','unused','Editor'),(2,'viewer','unused','Viewer');
		INSERT INTO database_connections (id,name,host,database_name,db_username,password_encrypted,ssl_mode,created_by)
		VALUES (1,'Workflow','localhost','workflow','test','unused','disable',1);
		INSERT INTO groups (id,name) VALUES (1,'editors'),(2,'viewers');
		INSERT INTO user_groups (user_id,group_id) VALUES (1,1),(2,2);
		UPDATE groups SET leader_user_id=id;
		INSERT INTO group_database_access (group_id,database_id,access_level,granted_by)
		VALUES (1,1,'edit',1),(2,1,'view',1);
		INSERT INTO migrations (id,database_id,title,sql_content,created_by)
		VALUES (1,1,'Test definition','SELECT 1',1);
	`)
	service := migration.WorkflowService{DB: db}
	// Validator SQL belum tersedia; test ini hanya memeriksa workflow approval.
	service.Validate = func(context.Context, *sql.Tx, int64, migration.RequestInput) (migration.DefinitionSnapshot, error) {
		return migration.DefinitionSnapshot{MigrationID: 1, DatabaseID: 1, Revision: 1,
			Title: "Test definition", SQLUp: "SELECT 1", Mode: "normal", Irreversible: true}, nil
	}
	ctx := context.Background()
	editor, viewer := &auth.User{ID: 1}, &auth.User{ID: 2}
	request, err := service.Create(ctx, editor, 1, migration.RequestInput{MigrationID: 1, Revision: 1, Operation: "up"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(ctx, editor, 1, request.ID); err != nil {
		t.Fatal(err)
	}
	approval, err := service.Approve(ctx, editor, 1, request.ID)
	if err != nil || approval.ReviewStatus != "pending_review" {
		t.Fatalf("satu group belum approve: %+v, %v", approval, err)
	}
	if _, err := service.Execute(ctx, editor, 1, request.ID); !errors.Is(err, migration.ErrApprovalIncomplete) {
		t.Fatalf("execute sebelum approval lengkap: %v", err)
	}
	approval, err = service.Approve(ctx, viewer, 1, request.ID)
	if err != nil || approval.ReviewStatus != "approved" {
		t.Fatalf("approval semua leader termasuk View: %+v, %v", approval, err)
	}
	if _, err := service.Execute(ctx, viewer, 1, request.ID); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("user View tidak boleh execute: %v", err)
	}
	if _, err := service.Execute(ctx, editor, 1, request.ID); !errors.Is(err, migration.ErrWorkflowUnavailable) {
		t.Fatalf("queue belum tersedia: %v", err)
	}
}
