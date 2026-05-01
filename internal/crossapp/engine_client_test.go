package crossapp

import (
	"reflect"
	"testing"
)

func TestSortedKeys(t *testing.T) {
	t.Parallel()

	got := sortedKeys(map[string]string{
		"zeta":  "1",
		"alpha": "2",
		"beta":  "3",
	})
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedKeys() = %v, want %v", got, want)
	}
}

func TestSortedKeysNil(t *testing.T) {
	t.Parallel()

	got := sortedKeys(nil)
	if len(got) != 0 {
		t.Fatalf("sortedKeys(nil) len = %d, want 0", len(got))
	}
}
