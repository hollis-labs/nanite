package pluginapi

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/hollis-labs/plugin-sdk/manifest"
)

const DataExportProtocol = 1
const MaxDataExportBytes = 128 << 20
const MaxDataExportRows = 1000000

// DataSnapshot preserves a core-table snapshot in a plugin's persistent DataDir.
// SourceID separates workspaces sharing that directory. The host commits a
// receipt with the table drop; uncommitted files must never be imported.
type DataSnapshot struct {
	Protocol int          `json:"protocol"`
	PluginID string       `json:"plugin_id"`
	Feature  string       `json:"feature"`
	SourceID string       `json:"source_id"`
	Columns  []string     `json:"columns"`
	Rows     [][]DataCell `json:"rows"`
}

// DataCell preserves SQLite storage classes exactly. Text carries literal text,
// decimal integers, round-trip floats or base64 blobs. text_bytes preserves
// non-UTF-8 SQLite text as base64. NULL has empty Text.
// Integers are strings so JavaScript decoding cannot round them above 2^53.
type DataCell struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

func (cell DataCell) Value() (any, error) {
	switch cell.Kind {
	case "null":
		if cell.Text != "" {
			return nil, fmt.Errorf("pluginapi: nonempty NULL cell")
		}
		return nil, nil
	case "text":
		if !utf8.ValidString(cell.Text) {
			return nil, fmt.Errorf("pluginapi: invalid UTF-8 text cell")
		}
		return cell.Text, nil
	case "text_bytes":
		raw, err := base64.StdEncoding.Strict().DecodeString(cell.Text)
		return string(raw), err
	case "integer":
		return strconv.ParseInt(cell.Text, 10, 64)
	case "real":
		value, err := strconv.ParseFloat(cell.Text, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("pluginapi: invalid real cell")
		}
		return value, nil
	case "blob":
		return base64.StdEncoding.Strict().DecodeString(cell.Text)
	default:
		return nil, fmt.Errorf("pluginapi: unknown data cell kind")
	}
}

// DataExport includes a checksum of the canonical snapshot encoding. Importers
// also compare the committed host receipt to PluginID, Feature, SourceID, digest
// and row count before changing their database in one transaction.
type DataExport struct {
	Snapshot DataSnapshot `json:"snapshot"`
	SHA256   string       `json:"sha256"`
}

type DataExportReceipt struct {
	PluginID string `json:"plugin_id"`
	Feature  string `json:"feature"`
	SourceID string `json:"source_id"`
	Path     string `json:"path"` // relative to InitParams.DataDir
	SHA256   string `json:"sha256"`
	RowCount int    `json:"row_count"`
}

var dataColumn = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

func (snapshot DataSnapshot) Validate() error {
	if snapshot.Protocol != DataExportProtocol || !manifest.ValidID(snapshot.PluginID) || !slug.MatchString(snapshot.Feature) || len(snapshot.Feature) > 64 || !validQuerySession(snapshot.SourceID) || len(snapshot.Columns) == 0 || len(snapshot.Columns) > 256 || snapshot.Rows == nil || len(snapshot.Rows) > MaxDataExportRows {
		return fmt.Errorf("pluginapi: invalid data snapshot")
	}
	seen := map[string]bool{}
	for _, column := range snapshot.Columns {
		if !dataColumn.MatchString(column) || seen[column] {
			return fmt.Errorf("pluginapi: invalid or duplicate data column")
		}
		seen[column] = true
	}
	for _, row := range snapshot.Rows {
		if len(row) != len(snapshot.Columns) {
			return fmt.Errorf("pluginapi: data row width differs from columns")
		}
		for _, cell := range row {
			if _, err := cell.Value(); err != nil {
				return fmt.Errorf("pluginapi: invalid data cell: %w", err)
			}
		}
	}
	return nil
}

type dataExportHeader struct {
	Protocol int      `json:"protocol"`
	PluginID string   `json:"plugin_id"`
	Feature  string   `json:"feature"`
	SourceID string   `json:"source_id"`
	Columns  []string `json:"columns"`
	RowCount int      `json:"row_count"`
	SHA256   string   `json:"sha256"`
}
type dataExportRow struct {
	Cells []DataCell `json:"cells"`
}

func snapshotDigest(snapshot DataSnapshot) (string, error) {
	if err := snapshot.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// EncodeDataExport emits JSON Lines: a bounded strict header followed by one
// bounded strict row per line. Whole files can exceed the SDK manifest cap;
// each line remains within it and uses the shared ambiguity checks.
func EncodeDataExport(snapshot DataSnapshot) ([]byte, error) {
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		return nil, err
	}
	header := dataExportHeader{Protocol: snapshot.Protocol, PluginID: snapshot.PluginID, Feature: snapshot.Feature, SourceID: snapshot.SourceID, Columns: snapshot.Columns, RowCount: len(snapshot.Rows), SHA256: digest}
	raw, err := json.Marshal(header)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(raw)
	out.WriteByte('\n')
	for _, row := range snapshot.Rows {
		raw, err = json.Marshal(dataExportRow{Cells: row})
		if err != nil {
			return nil, err
		}
		if len(raw) > manifest.MaxBytes || out.Len()+len(raw)+1 > MaxDataExportBytes {
			return nil, fmt.Errorf("pluginapi: data export exceeds limit")
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

func DecodeDataExport(reader io.Reader) (DataExport, error) {
	bounded := &io.LimitedReader{R: reader, N: MaxDataExportBytes + 1}
	scanner := bufio.NewScanner(bounded)
	scanner.Buffer(make([]byte, 4096), manifest.MaxBytes+1)
	if !scanner.Scan() {
		return DataExport{}, fmt.Errorf("pluginapi: data export header absent")
	}
	var header dataExportHeader
	if err := manifest.DecodeExtension(scanner.Bytes(), &header); err != nil {
		return DataExport{}, err
	}
	if header.RowCount < 0 || header.RowCount > MaxDataExportRows {
		return DataExport{}, fmt.Errorf("pluginapi: invalid data export row count")
	}
	result := DataExport{SHA256: header.SHA256, Snapshot: DataSnapshot{Protocol: header.Protocol, PluginID: header.PluginID, Feature: header.Feature, SourceID: header.SourceID, Columns: header.Columns, Rows: make([][]DataCell, 0)}}
	for scanner.Scan() {
		if len(result.Snapshot.Rows) >= header.RowCount {
			return DataExport{}, fmt.Errorf("pluginapi: data export has excess rows")
		}
		var row dataExportRow
		if err := manifest.DecodeExtension(scanner.Bytes(), &row); err != nil {
			return DataExport{}, err
		}
		result.Snapshot.Rows = append(result.Snapshot.Rows, row.Cells)
	}
	if err := scanner.Err(); err != nil {
		return DataExport{}, err
	}
	if bounded.N <= 0 || len(result.Snapshot.Rows) != header.RowCount {
		return DataExport{}, fmt.Errorf("pluginapi: data export truncated or exceeds limit")
	}
	digest, err := snapshotDigest(result.Snapshot)
	if err != nil {
		return DataExport{}, err
	}
	if result.SHA256 != digest {
		return DataExport{}, fmt.Errorf("pluginapi: data export checksum differs")
	}
	return result, nil
}

func (receipt DataExportReceipt) Verify(export DataExport) error {
	snapshot := export.Snapshot
	if receipt.PluginID != snapshot.PluginID || receipt.Feature != snapshot.Feature || receipt.SourceID != snapshot.SourceID || receipt.SHA256 != export.SHA256 || receipt.RowCount != len(snapshot.Rows) || !bundlePath(receipt.Path) {
		return fmt.Errorf("pluginapi: data export differs from committed receipt")
	}
	digest, err := snapshotDigest(snapshot)
	if err != nil {
		return err
	}
	if digest != receipt.SHA256 {
		return fmt.Errorf("pluginapi: data export checksum differs")
	}
	return nil
}
