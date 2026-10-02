// Package dataexport transfers retired core-table data into plugin persistence.
// It is invoked by an extraction migration only after the plugin release exists.
package dataexport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
)

type Spec struct{ PluginID, Feature, SourceID, Table string }

var protectedTables = map[string]bool{"sessions": true, "messages": true, "agent_profiles": true, "agent_instances": true, "goose_db_version": true, "plugin_core_exports": true}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// SourceID separates database paths sharing a global plugin DataDir. Receipts
// retain the original ID if a database is later moved with its ledger intact.
func SourceID(databasePath string) (string, error) {
	absolute, err := filepath.Abs(databasePath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absolute))
	return hex.EncodeToString(sum[:16]), nil
}

const ledgerDDL = `CREATE TABLE IF NOT EXISTS plugin_core_exports (
 plugin_id TEXT NOT NULL, feature TEXT NOT NULL, source_id TEXT NOT NULL,
 relative_path TEXT NOT NULL, sha256 TEXT NOT NULL, row_count INTEGER NOT NULL CHECK(row_count >= 0),
 PRIMARY KEY (plugin_id, feature, source_id)
)`

// ExportAndDrop requires the caller's write transaction. It writes, fsyncs and
// re-reads a content-addressed export before recording a receipt and dropping
// the table in that SAME transaction. The caller must roll back on any error.
// Only committed ledger rows authorize import; orphan files are harmless.
// No feature invokes this helper until its release/adoption and reader cutover.
func ExportAndDrop(ctx context.Context, tx *sql.Tx, spec Spec, dataDir string) (pluginapi.DataExportReceipt, error) {
	return exportAndDrop(ctx, tx, spec, dataDir, publishSnapshot)
}

type snapshotPublisher func(string, string, []byte) error

func exportAndDrop(ctx context.Context, tx *sql.Tx, spec Spec, dataDir string, publish snapshotPublisher) (result pluginapi.DataExportReceipt, retErr error) {
	if tx == nil || !identifier.MatchString(spec.Table) || protectedTables[spec.Table] || strings.HasPrefix(spec.Table, "sqlite_") || !filepath.IsAbs(dataDir) {
		return pluginapi.DataExportReceipt{}, fmt.Errorf("invalid core export request")
	}
	if _, checkErr := tx.ExecContext(ctx, `SAVEPOINT nanite_core_export`); checkErr != nil {
		return pluginapi.DataExportReceipt{}, checkErr
	}
	defer func() {
		if retErr == nil {
			_, retErr = tx.ExecContext(ctx, `RELEASE SAVEPOINT nanite_core_export`)
			if retErr == nil {
				return
			}
		}
		cleanup := context.WithoutCancel(ctx)
		_, rollbackErr := tx.ExecContext(cleanup, `ROLLBACK TO SAVEPOINT nanite_core_export`)
		_, releaseErr := tx.ExecContext(cleanup, `RELEASE SAVEPOINT nanite_core_export`)
		retErr = errors.Join(retErr, rollbackErr, releaseErr)
	}()
	snapshot := pluginapi.DataSnapshot{Protocol: pluginapi.DataExportProtocol, PluginID: spec.PluginID, Feature: spec.Feature, SourceID: spec.SourceID, Columns: []string{"control"}, Rows: [][]pluginapi.DataCell{}}
	if checkErr := snapshot.Validate(); checkErr != nil {
		return pluginapi.DataExportReceipt{}, checkErr
	}
	var exists bool
	if checkErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, spec.Table).Scan(&exists); checkErr != nil {
		return pluginapi.DataExportReceipt{}, checkErr
	}
	if !exists {
		receipt, err := readReceipt(ctx, tx, spec)
		if err != nil {
			return receipt, fmt.Errorf("missing core table has no committed export: %w", err)
		}
		if checkErr := verifyFile(dataDir, receipt); checkErr != nil {
			return receipt, checkErr
		}
		return receipt, nil
	}
	columns, err := tableColumns(ctx, tx, spec.Table)
	if err != nil {
		return pluginapi.DataExportReceipt{}, err
	}
	snapshot.Columns = columns
	snapshot.Rows, err = readRows(ctx, tx, spec.Table, columns)
	if err != nil {
		return pluginapi.DataExportReceipt{}, err
	}
	var rowCount int
	// #nosec G202 -- the table name passed validation before any SQL is formed.
	if checkErr := tx.QueryRowContext(ctx, `SELECT count(*) FROM "`+spec.Table+`"`).Scan(&rowCount); checkErr != nil {
		return pluginapi.DataExportReceipt{}, checkErr
	}
	if rowCount != len(snapshot.Rows) {
		return pluginapi.DataExportReceipt{}, fmt.Errorf("core export row count differs")
	}
	raw, err := pluginapi.EncodeDataExport(snapshot)
	if err != nil {
		return pluginapi.DataExportReceipt{}, err
	}
	decoded, err := pluginapi.DecodeDataExport(bytes.NewReader(raw))
	if err != nil {
		return pluginapi.DataExportReceipt{}, err
	}
	relative := filepath.ToSlash(filepath.Join("core-imports", spec.SourceID, spec.Feature+"-"+decoded.SHA256+".jsonl"))
	receipt := pluginapi.DataExportReceipt{PluginID: spec.PluginID, Feature: spec.Feature, SourceID: spec.SourceID, Path: relative, SHA256: decoded.SHA256, RowCount: len(snapshot.Rows)}
	if checkErr := publish(dataDir, relative, raw); checkErr != nil {
		return receipt, checkErr
	}
	if checkErr := verifyFile(dataDir, receipt); checkErr != nil {
		return receipt, checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return receipt, checkErr
	}
	if _, checkErr := tx.ExecContext(ctx, ledgerDDL); checkErr != nil {
		return receipt, checkErr
	}
	if _, checkErr := tx.ExecContext(ctx, `INSERT INTO plugin_core_exports (plugin_id,feature,source_id,relative_path,sha256,row_count) VALUES (?,?,?,?,?,?)`, receipt.PluginID, receipt.Feature, receipt.SourceID, receipt.Path, receipt.SHA256, receipt.RowCount); checkErr != nil {
		return receipt, fmt.Errorf("record core export: %w", checkErr)
	}
	// #nosec G202 -- the host's table identifier is validated above, never supplied by a plugin.
	if _, checkErr := tx.ExecContext(ctx, `DROP TABLE "`+spec.Table+`"`); checkErr != nil {
		return receipt, fmt.Errorf("retire exported table: %w", checkErr)
	}
	return receipt, nil
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	// #nosec G202 -- table was validated as a bare ASCII identifier before this call.
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var columns []string
	for rows.Next() {
		var cid, notNull, primary int
		var name, kind string
		var defaultValue any
		if checkErr := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primary); checkErr != nil {
			return nil, checkErr
		}
		if !identifier.MatchString(name) {
			return nil, fmt.Errorf("unsupported core column name")
		}
		columns = append(columns, name)
	}
	if checkErr := rows.Err(); checkErr != nil {
		return nil, checkErr
	}
	if len(columns) == 0 || len(columns) > 256 {
		return nil, fmt.Errorf("unsupported core table shape")
	}
	return columns, nil
}

func readRows(ctx context.Context, tx *sql.Tx, table string, columns []string) ([][]pluginapi.DataCell, error) {
	quoted := make([]string, len(columns))
	for i, name := range columns {
		quoted[i] = `"` + name + `"`
	}
	list := strings.Join(quoted, ",")
	projection := make([]string, 0, len(columns)*2)
	for _, column := range quoted {
		// CAST prevents modernc's declared DATETIME metadata from converting
		// stored text into time.Time (which would rewrite its original bytes).
		projection = append(projection, `typeof(`+column+`)`, `CASE WHEN typeof(`+column+`)='text' THEN CAST(`+column+` AS BLOB) ELSE `+column+` END`)
	}

	// #nosec G202 -- only validated, host-owned identifiers form the query; there is no caller SQL.
	rows, err := tx.QueryContext(ctx, `SELECT `+strings.Join(projection, ",")+` FROM "`+table+`" ORDER BY `+list)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([][]pluginapi.DataCell, 0)
	totalBytes := 0
	for rows.Next() {
		if len(out) >= pluginapi.MaxDataExportRows {
			return nil, fmt.Errorf("core export row limit exceeded")
		}
		values := make([]any, len(columns)*2)
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if checkErr := rows.Scan(pointers...); checkErr != nil {
			return nil, checkErr
		}
		cells := make([]pluginapi.DataCell, len(columns))
		for i := range columns {
			kind, ok := values[i*2].(string)
			if !ok {
				return nil, fmt.Errorf("SQLite storage class is absent")
			}
			cell, err := encodeStorageCell(kind, values[i*2+1])
			if err != nil {
				return nil, err
			}
			if len(cell.Text) > manifest.MaxBytes {
				return nil, fmt.Errorf("core export cell exceeds limit")
			}
			cells[i] = cell
		}
		encoded, err := json.Marshal(cells)
		if err != nil {
			return nil, err
		}
		totalBytes += len(encoded) + 16
		if len(encoded)+16 > manifest.MaxBytes || totalBytes > pluginapi.MaxDataExportBytes {
			return nil, fmt.Errorf("core export byte limit exceeded")
		}
		out = append(out, cells)
	}
	return out, rows.Err()
}

func encodeStorageCell(kind string, value any) (pluginapi.DataCell, error) {
	if kind == "text" {
		raw, ok := value.([]byte)
		if !ok {
			return pluginapi.DataCell{}, fmt.Errorf("SQLite text was not projected as raw bytes")
		}
		return encodeCell(string(raw))
	}
	cell, err := encodeCell(value)
	if err != nil {
		return cell, err
	}
	if cell.Kind != kind {
		return pluginapi.DataCell{}, fmt.Errorf("SQLite storage class differs from observed value")
	}
	return cell, nil
}

func encodeCell(value any) (pluginapi.DataCell, error) {
	switch value := value.(type) {
	case nil:
		return pluginapi.DataCell{Kind: "null"}, nil
	case string:
		if !utf8.ValidString(value) {
			return pluginapi.DataCell{Kind: "text_bytes", Text: base64.StdEncoding.EncodeToString([]byte(value))}, nil
		}
		return pluginapi.DataCell{Kind: "text", Text: value}, nil
	case int64:
		return pluginapi.DataCell{Kind: "integer", Text: strconv.FormatInt(value, 10)}, nil
	case float64:
		return pluginapi.DataCell{Kind: "real", Text: strconv.FormatFloat(value, 'g', -1, 64)}, nil
	case []byte:
		return pluginapi.DataCell{Kind: "blob", Text: base64.StdEncoding.EncodeToString(value)}, nil
	default:
		return pluginapi.DataCell{}, fmt.Errorf("unsupported SQLite storage value %T", value)
	}
}

func readReceipt(ctx context.Context, tx *sql.Tx, spec Spec) (pluginapi.DataExportReceipt, error) {
	receipt := pluginapi.DataExportReceipt{PluginID: spec.PluginID, Feature: spec.Feature, SourceID: spec.SourceID}
	err := tx.QueryRowContext(ctx, `SELECT relative_path,sha256,row_count FROM plugin_core_exports WHERE plugin_id=? AND feature=? AND source_id=?`, spec.PluginID, spec.Feature, spec.SourceID).Scan(&receipt.Path, &receipt.SHA256, &receipt.RowCount)
	return receipt, err
}

func openDataRoot(dataDir string) (*os.Root, error) {
	info, err := os.Lstat(dataDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("plugin data root must be a real directory")
	}
	return os.OpenRoot(dataDir)
}

func verifyFile(dataDir string, receipt pluginapi.DataExportReceipt) error {
	root, err := openDataRoot(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(receipt.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > pluginapi.MaxDataExportBytes {
		return fmt.Errorf("core export must be a bounded regular file")
	}
	file, err := root.Open(receipt.Path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoded, err := pluginapi.DecodeDataExport(file)
	if err != nil {
		return err
	}
	return receipt.Verify(decoded)
}

func publishSnapshot(dataDir, relative string, raw []byte) (retErr error) {
	root, openErr := openDataRoot(dataDir)
	if openErr != nil {
		return openErr
	}
	defer func() { _ = root.Close() }()
	parent := filepath.Dir(relative)
	for _, directory := range []string{"core-imports", parent} {
		if checkErr := root.Mkdir(directory, 0700); checkErr != nil && !errors.Is(checkErr, os.ErrExist) {
			return checkErr
		}
		info, err := root.Lstat(directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("core export parent must be a real directory")
		}
	}
	if info, checkErr := root.Lstat(relative); checkErr == nil {
		if !info.Mode().IsRegular() || info.Size() != int64(len(raw)) {
			return fmt.Errorf("existing core export is not the expected regular file")
		}
		existingFile, err := root.Open(relative)
		if err != nil {
			return err
		}
		existing, err := io.ReadAll(io.LimitReader(existingFile, int64(len(raw))+1))
		closeErr := existingFile.Close()
		if closeErr != nil {
			return closeErr
		}
		if err != nil || !bytes.Equal(existing, raw) {
			return fmt.Errorf("existing core export differs")
		}
		return syncDirectories(root, dataDir, parent)
	} else if !errors.Is(checkErr, os.ErrNotExist) {
		return checkErr
	}
	nonce := make([]byte, 16)
	if _, checkErr := rand.Read(nonce); checkErr != nil {
		return checkErr
	}
	temporary := filepath.Join(parent, ".export-"+hex.EncodeToString(nonce))
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temporary) }()
	if n, checkErr := file.Write(raw); checkErr != nil || n != len(raw) {
		_ = file.Close()
		if checkErr != nil {
			return checkErr
		}
		return io.ErrShortWrite
	}
	if checkErr := file.Sync(); checkErr != nil {
		_ = file.Close()
		return checkErr
	}
	if checkErr := file.Close(); checkErr != nil {
		return checkErr
	}
	// Link publishes without replacing an existing file, even across retries.
	if checkErr := root.Link(temporary, relative); checkErr != nil {
		return checkErr
	}
	return syncDirectories(root, dataDir, parent)
}

func syncDirectories(root *os.Root, dataDir, parent string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	// Publish the file, newly created parent names and DataDir's own entry
	// durably before SQLite may commit its drop. Existing ancestors are synced
	// too, because callers can have created the layout earlier in this boot.
	for _, relative := range []string{parent, "core-imports", "."} {
		dir, err := root.Open(relative)
		if err != nil {
			return err
		}
		syncErr := dir.Sync()
		closeErr := dir.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for ancestor := filepath.Dir(dataDir); ; ancestor = filepath.Dir(ancestor) {
		// #nosec G304 -- ancestor comes from the host's absolute persistent DataDir, never plugin output.
		dir, err := os.Open(ancestor)
		if err != nil {
			return err
		}
		syncErr := dir.Sync()
		closeErr := dir.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if filepath.Dir(ancestor) == ancestor {
			break
		}
	}
	return nil
}
