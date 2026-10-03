package reflexes

import (
	"reflect"
	"testing"
)

// Fill every exported field recursively, with distinct non-zero signal values.
// Adding a host State field must make this fail until its adapter carries it.
func TestLibraryState_PreservesEveryExportedHostField(t *testing.T) {
	var state State
	populatedSignalValue(t, reflect.ValueOf(&state).Elem(), "State")
	host := reflect.ValueOf(state)
	converted := reflect.ValueOf(libraryState(state))
	for i := 0; i < host.NumField(); i++ {
		field := host.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		want := host.Field(i)
		if want.IsZero() {
			t.Fatalf("fixture left %s zero", field.Name)
		}
		got := converted.FieldByName(field.Name)
		if !got.IsValid() {
			t.Fatalf("library State lacks host field %s", field.Name)
		}
		if qualificationJSON(t, want.Interface()) != qualificationJSON(t, got.Interface()) {
			t.Errorf("%s lost in conversion: host=%s library=%s", field.Name, qualificationJSON(t, want.Interface()), qualificationJSON(t, got.Interface()))
		}
	}
}

func populatedSignalValue(t *testing.T, value reflect.Value, path string) {
	t.Helper()
	switch value.Kind() {
	case reflect.String:
		value.SetString(path)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(7)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.IsExported() {
				populatedSignalValue(t, value.Field(i), path+"."+field.Name)
			}
		}
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		populatedSignalValue(t, value.Index(0), path+"[0]")
	default:
		t.Fatalf("add fixture support for %s (%s)", path, value.Kind())
	}
}
