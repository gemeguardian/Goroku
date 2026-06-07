package goroku

import "testing"

func TestDatabaseDeepCopyClonesNestedMapsAndSlices(t *testing.T) {
	db := &Database{}
	src := map[string]map[string]interface{}{
		"owner": {
			"nested": map[string]interface{}{"key": "original"},
			"slice":  []interface{}{map[string]interface{}{"item": "original"}},
		},
	}

	copy := db.deepCopy(src)
	copy["owner"]["nested"].(map[string]interface{})["key"] = "changed"
	copy["owner"]["slice"].([]interface{})[0].(map[string]interface{})["item"] = "changed"

	if got := src["owner"]["nested"].(map[string]interface{})["key"]; got != "original" {
		t.Fatalf("deepCopy shared nested map with source, got %v", got)
	}
	if got := src["owner"]["slice"].([]interface{})[0].(map[string]interface{})["item"]; got != "original" {
		t.Fatalf("deepCopy shared nested slice value with source, got %v", got)
	}
}
