package contextbroker

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PCCSource retrieves context from the Project Context Cache (PCC) files.
// PCC files are read-only 6-file sets under .agentrc/pcc/global/<project>/.
type PCCSource struct {
	// BasePath is the root directory containing PCC files.
	// Default: .agentrc/pcc/global/
	BasePath string
}

// NewPCCSource creates a PCCSource with the given base path.
func NewPCCSource(basePath string) *PCCSource {
	return &PCCSource{BasePath: basePath}
}

func (s *PCCSource) Name() string { return "pcc" }

// pccFileRelevance maps PCC filename prefixes to their relevance for each intent type.
var pccFileRelevance = map[string]map[string]float64{
	IntentWriteCode: {
		"01_conventions":  0.9,
		"02_architecture": 0.8,
		"00_project":      0.6,
		"03_active_work":  0.5,
		"05_decisions":    0.4,
		"04_pitfalls":     0.7,
	},
	IntentDebugIssue: {
		"04_pitfalls":     0.9,
		"02_architecture": 0.7,
		"01_conventions":  0.5,
		"03_active_work":  0.6,
		"00_project":      0.3,
		"05_decisions":    0.4,
	},
	IntentPlanFeature: {
		"02_architecture": 0.9,
		"05_decisions":    0.8,
		"03_active_work":  0.7,
		"00_project":      0.6,
		"01_conventions":  0.4,
		"04_pitfalls":     0.3,
	},
	IntentRecallDecision: {
		"05_decisions":    0.9,
		"02_architecture": 0.6,
		"03_active_work":  0.5,
		"00_project":      0.4,
		"01_conventions":  0.3,
		"04_pitfalls":     0.3,
	},
	IntentBootProject: {
		"00_project":      0.9,
		"02_architecture": 0.8,
		"01_conventions":  0.7,
		"03_active_work":  0.6,
		"05_decisions":    0.5,
		"04_pitfalls":     0.4,
	},
}

func (s *PCCSource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.BasePath == "" {
		return nil, fmt.Errorf("pcc source: no base path configured")
	}

	// Determine which project directory to read.
	projectDir := s.resolveProjectDir(intent.Scope)
	if projectDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pcc dir %s: %w", projectDir, err)
	}

	// Read and score all PCC files.
	var items []ContextItem
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(projectDir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			log.Printf("contextbroker/pcc: failed to read %s: %v", filePath, err)
			continue
		}

		if len(content) == 0 {
			continue
		}

		tokens := EstimateTokens(string(content))
		relevance := s.scoreFile(entry.Name(), intent)

		items = append(items, ContextItem{
			Source:        "pcc",
			Key:           fmt.Sprintf("%s/%s", filepath.Base(projectDir), entry.Name()),
			Content:       string(content),
			TokenEstimate: tokens,
			Relevance:     relevance,
			Metadata: map[string]string{
				"file": filePath,
			},
		})
	}

	// Sort by relevance (highest first).
	sort.Slice(items, func(i, j int) bool {
		return items[i].Relevance > items[j].Relevance
	})

	// Trim to budget.
	var kept []ContextItem
	usedTokens := 0
	for _, item := range items {
		if usedTokens+item.TokenEstimate > budget {
			break
		}
		kept = append(kept, item)
		usedTokens += item.TokenEstimate
	}

	return kept, nil
}

// resolveProjectDir finds the PCC directory for the given scope.
func (s *PCCSource) resolveProjectDir(scope string) string {
	if scope == "" {
		// No scope — try to find any project dir.
		entries, err := os.ReadDir(s.BasePath)
		if err != nil {
			return ""
		}
		for _, entry := range entries {
			if entry.IsDir() {
				return filepath.Join(s.BasePath, entry.Name())
			}
		}
		return ""
	}

	// Try exact match first.
	dir := filepath.Join(s.BasePath, scope)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}

	return ""
}

// scoreFile assigns a relevance score based on the PCC filename and intent.
func (s *PCCSource) scoreFile(filename string, intent Intent) float64 {
	// Look up intent-specific relevance.
	if relevanceMap, ok := pccFileRelevance[intent.Type]; ok {
		for prefix, score := range relevanceMap {
			if strings.HasPrefix(filename, prefix) {
				return score
			}
		}
	}

	// Default: moderate relevance.
	return 0.5
}
