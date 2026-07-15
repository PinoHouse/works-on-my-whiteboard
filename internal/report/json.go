package report

import (
	"encoding/json"
	"fmt"
	"io"
)

func WriteJSON(w io.Writer, model Model) error {
	if w == nil {
		return fmt.Errorf("write report JSON: nil writer")
	}
	if err := validateReportDynamicText(model); err != nil {
		return fmt.Errorf("write report JSON: %w", err)
	}
	encoded, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return fmt.Errorf("write report JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := writeExact(w, encoded); err != nil {
		return fmt.Errorf("write report JSON: %w", err)
	}
	return nil
}

func writeExact(w io.Writer, data []byte) error {
	written, err := w.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
