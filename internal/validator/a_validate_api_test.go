package validator

import (
	"testing"

	"github.com/PinoHouse/works-on-my-whiteboard/internal/catalog"
)

func TestValidateAPIContract(t *testing.T) {
	report := Validate(&catalog.Catalog{}, "development")
	if report.Diagnostics == nil {
		t.Fatal("Validate() diagnostics must be a non-nil deterministic list")
	}
}
