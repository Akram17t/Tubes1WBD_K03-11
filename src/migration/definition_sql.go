package migration

import (
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Pemeriksaan di file ini bekerja pada AST parser PostgreSQL, bukan pada teks, sehingga titik koma
// atau kata COMMIT di dalam string literal tidak salah dikenali.
// Batasannya: SQL dinamis yang dirakit saat runtime di dalam function tidak terlihat oleh parser.
// Hak akun database target tetap menjadi batas keamanan terakhir.

const internalPrefix = "_dbplatform"

func mentionsInternalMetadata(value string) bool {
	return strings.Contains(strings.ToLower(value), internalPrefix)
}

// parseStatements mem-parse seluruh script dan menolak script tanpa statement.
func parseStatements(field, query string) ([]*pg.RawStmt, error) {
	tree, err := pgquery.Parse(query)
	if err != nil {
		return nil, invalidSQL(field, "Sintaks SQL tidak valid: "+err.Error())
	}
	if len(tree.Stmts) == 0 {
		return nil, invalidSQL(field, "SQL tidak berisi statement.")
	}
	return tree.Stmts, nil
}

// walkAST mengunjungi setiap node beserta setiap nilai string, termasuk statement bersarang
// seperti DML di dalam CTE atau EXPLAIN.
func walkAST(message protoreflect.Message, node func(any), text func(string)) {
	node(message.Interface())
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		visit := func(item protoreflect.Value) {
			switch field.Kind() {
			case protoreflect.MessageKind:
				walkAST(item.Message(), node, text)
			case protoreflect.StringKind:
				text(item.String())
			}
		}
		if field.IsList() {
			list := value.List()
			for i := range list.Len() {
				visit(list.Get(i))
			}
		} else {
			visit(value)
		}
		return true
	})
}

// checkStatements menerapkan larangan yang berlaku pada mode normal maupun batch.
func checkStatements(field string, statements []*pg.RawStmt) error {
	reason := ""
	reject := func(message string) {
		if reason == "" {
			reason = message
		}
	}
	for _, statement := range statements {
		walkAST(statement.ProtoReflect(), func(node any) {
			if message := forbiddenNode(node); message != "" {
				reject(message)
			}
		}, func(value string) {
			if mentionsInternalMetadata(value) {
				reject("SQL tidak boleh menyentuh metadata internal platform (" + internalPrefix + ").")
			}
		})
	}
	if reason != "" {
		return invalidSQL(field, reason)
	}
	return nil
}

// forbiddenNode mengembalikan alasan penolakan, atau string kosong bila node diizinkan.
func forbiddenNode(node any) string {
	const (
		transaction    = "Statement kontrol transaksi (BEGIN, COMMIT, ROLLBACK, SAVEPOINT) tidak diizinkan."
		session        = "Statement yang mengubah role, session, atau koneksi tidak diizinkan."
		server         = "Operasi tingkat server (database, tablespace, subscription, ALTER SYSTEM) tidak diizinkan."
		opaque         = "DO, CALL, PREPARE, dan EXECUTE tidak didukung karena isinya tidak dapat diperiksa."
		nonTransaction = "Statement yang tidak dapat berjalan di dalam transaction (misalnya CONCURRENTLY atau VACUUM) tidak diizinkan."
		publicSchema   = "Schema public memuat metadata platform sehingga tidak boleh dihapus atau diganti nama."
	)
	switch statement := node.(type) {
	case *pg.TransactionStmt:
		return transaction
	case *pg.VariableSetStmt:
		name := strings.ToLower(statement.Name)
		if statement.Kind == pg.VariableSetKind_VAR_RESET_ALL || name == "role" || name == "session_authorization" {
			return session
		}
		if strings.HasPrefix(name, "transaction") || strings.HasPrefix(name, "session characteristics") ||
			strings.HasPrefix(name, "default_transaction") {
			return transaction
		}
	case *pg.DiscardStmt, *pg.ListenStmt, *pg.UnlistenStmt, *pg.LoadStmt,
		*pg.CreateRoleStmt, *pg.AlterRoleStmt, *pg.AlterRoleSetStmt, *pg.DropRoleStmt,
		*pg.GrantRoleStmt, *pg.ReassignOwnedStmt, *pg.DropOwnedStmt:
		return session
	case *pg.CreatedbStmt, *pg.DropdbStmt, *pg.AlterDatabaseStmt, *pg.AlterDatabaseSetStmt,
		*pg.AlterDatabaseRefreshCollStmt, *pg.AlterSystemStmt, *pg.CreateTableSpaceStmt,
		*pg.DropTableSpaceStmt, *pg.CreateSubscriptionStmt, *pg.AlterSubscriptionStmt,
		*pg.DropSubscriptionStmt:
		return server
	case *pg.DoStmt, *pg.CallStmt, *pg.PrepareStmt, *pg.ExecuteStmt, *pg.DeallocateStmt:
		return opaque
	case *pg.CopyStmt:
		return "COPY tidak didukung oleh eksekutor migration."
	case *pg.VacuumStmt:
		if statement.IsVacuumcmd {
			return nonTransaction
		}
	case *pg.IndexStmt:
		if statement.Concurrent {
			return nonTransaction
		}
	case *pg.PartitionCmd:
		if statement.Concurrent {
			return nonTransaction
		}
	case *pg.ClusterStmt:
		if statement.Relation == nil {
			return nonTransaction
		}
	case *pg.ReindexStmt:
		if statement.Kind != pg.ReindexObjectType_REINDEX_OBJECT_TABLE &&
			statement.Kind != pg.ReindexObjectType_REINDEX_OBJECT_INDEX {
			return nonTransaction
		}
		for _, param := range statement.Params {
			if strings.EqualFold(param.GetDefElem().GetDefname(), "concurrently") {
				return nonTransaction
			}
		}
	case *pg.DropStmt:
		if statement.Concurrent {
			return nonTransaction
		}
		if statement.RemoveType == pg.ObjectType_OBJECT_SCHEMA {
			for _, object := range statement.Objects {
				if object.GetString_().GetSval() == "public" {
					return publicSchema
				}
			}
		}
	case *pg.RenameStmt:
		if statement.RenameType == pg.ObjectType_OBJECT_SCHEMA && statement.Subname == "public" {
			return publicSchema
		}
	case *pg.FuncCall:
		if forbiddenFunction(statement) {
			return "Function pengatur session, advisory lock, atau koneksi lain tidak diizinkan."
		}
	}
	return ""
}

// forbiddenFunction menolak function yang dapat melepas lock eksekutor atau mengganti session.
func forbiddenFunction(call *pg.FuncCall) bool {
	if len(call.Funcname) == 0 {
		return false
	}
	name := strings.ToLower(call.Funcname[len(call.Funcname)-1].GetString_().GetSval())
	switch name {
	case "set_config", "pg_terminate_backend", "pg_cancel_backend", "pg_reload_conf":
		return true
	}
	return strings.HasPrefix(name, "pg_advisory") || strings.HasPrefix(name, "pg_try_advisory") ||
		strings.HasPrefix(name, "dblink")
}

func countParams(statements []*pg.RawStmt) (count int) {
	for _, statement := range statements {
		walkAST(statement.ProtoReflect(), func(node any) {
			if _, ok := node.(*pg.ParamRef); ok {
				count++
			}
		}, func(string) {})
	}
	return count
}

// checkNormalSQL menerima script DDL/DML transactional tanpa parameter.
func checkNormalSQL(field, query string) error {
	statements, err := parseStatements(field, query)
	if err != nil {
		return err
	}
	if err := checkStatements(field, statements); err != nil {
		return err
	}
	if countParams(statements) != 0 {
		return invalidSQL(field, "Mode normal tidak menerima parameter seperti $1.")
	}
	return nil
}

// checkBatchSQL hanya menerima satu UPDATE yang cakupannya sepenuhnya ditentukan daftar ID platform:
// UPDATE schema.tabel SET kolom = ... WHERE key = ANY($1::bigint[])
func checkBatchSQL(field, query string, config BatchConfig) error {
	statements, err := parseStatements(field, query)
	if err != nil {
		return err
	}
	if err := checkStatements(field, statements); err != nil {
		return err
	}
	if len(statements) != 1 || statements[0].Stmt.GetUpdateStmt() == nil {
		return invalidSQL(field, "Mode batch hanya menerima tepat satu statement UPDATE.")
	}
	update := statements[0].Stmt.GetUpdateStmt()
	if update.WithClause != nil || len(update.FromClause) != 0 || len(update.ReturningList) != 0 {
		return invalidSQL(field, "UPDATE batch tidak boleh memakai WITH, FROM, atau RETURNING.")
	}
	relation := update.Relation
	if relation.GetSchemaname() != config.Schema || relation.GetRelname() != config.Table {
		return invalidSQL(field, "UPDATE batch harus menulis schema.tabel yang sama dengan batch_config.")
	}
	for _, target := range update.TargetList {
		if target.GetResTarget().GetName() == config.KeyColumn {
			return invalidSQL(field, "UPDATE batch tidak boleh mengubah kolom key.")
		}
	}
	alias := relation.GetRelname()
	if relation.Alias != nil {
		alias = relation.Alias.Aliasname
	}
	if countParams(statements) != 1 || !isScopeFilter(update.WhereClause, alias, config.KeyColumn) {
		return invalidSQL(field, "WHERE harus tepat berbentuk: "+config.KeyColumn+" = ANY($1::bigint[])")
	}
	return nil
}

// isScopeFilter memeriksa bentuk key = ANY($1::bigint[]) tanpa kondisi lain yang dapat memperluas cakupan.
func isScopeFilter(where *pg.Node, alias, key string) bool {
	expression := where.GetAExpr()
	if expression == nil || expression.Kind != pg.A_Expr_Kind_AEXPR_OP_ANY ||
		len(expression.Name) != 1 || expression.Name[0].GetString_().GetSval() != "=" {
		return false
	}
	column := expression.Lexpr.GetColumnRef().GetFields()
	switch len(column) {
	case 1:
	case 2:
		if column[0].GetString_().GetSval() != alias {
			return false
		}
	default:
		return false
	}
	if column[len(column)-1].GetString_().GetSval() != key {
		return false
	}
	cast := expression.Rexpr.GetTypeCast()
	if cast == nil || cast.Arg.GetParamRef().GetNumber() != 1 || len(cast.TypeName.GetArrayBounds()) != 1 {
		return false
	}
	names := cast.TypeName.GetNames()
	if len(names) == 2 && names[0].GetString_().GetSval() != "pg_catalog" {
		return false
	}
	if len(names) < 1 || len(names) > 2 {
		return false
	}
	return names[len(names)-1].GetString_().GetSval() == "int8"
}
