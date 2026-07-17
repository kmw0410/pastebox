package main

import (
	"encoding/json"
	"fmt"
	"io"
)

func writeUploadOutput(w io.Writer, result uploadResponse, raw []byte, opts uploadOptions) error {
	if opts.quiet {
		_, err := fmt.Fprintln(w, result.URL)
		return err
	}
	if opts.jsonOutput {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	if _, err := fmt.Fprintf(w, "url: %s\n", result.URL); err != nil {
		return err
	}
	if result.Expires != "" {
		if _, err := fmt.Fprintf(w, "expires: %s\n", result.Expires); err != nil {
			return err
		}
	}
	if result.Password != "" {
		if _, err := fmt.Fprintf(w, "password: %s\n", result.Password); err != nil {
			return err
		}
	}
	if result.Manage != "" {
		if _, err := fmt.Fprintf(w, "manage: %s\n", result.Manage); err != nil {
			return err
		}
	}
	if result.Delete != "" {
		if _, err := fmt.Fprintf(w, "delete: %s\n", result.Delete); err != nil {
			return err
		}
	}
	return nil
}
