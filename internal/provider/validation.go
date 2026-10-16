package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func validateExecution(script, hash, directory types.String, environment types.Map, timeout types.Int64) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if !script.IsUnknown() && !script.IsNull() && (!filepath.IsAbs(script.ValueString()) || strings.ContainsRune(script.ValueString(), 0)) {
		diagnostics.AddError("Invalid script path", "script must be an absolute path without NUL.")
	}
	if !directory.IsUnknown() && !directory.IsNull() && (!filepath.IsAbs(directory.ValueString()) || strings.ContainsRune(directory.ValueString(), 0)) {
		diagnostics.AddError("Invalid working directory", "working_directory must be an absolute path without NUL.")
	}
	if !hash.IsUnknown() && !hash.IsNull() {
		decoded, err := hex.DecodeString(hash.ValueString())
		if err != nil || len(decoded) != sha256.Size || hash.ValueString() != strings.ToLower(hash.ValueString()) {
			diagnostics.AddError("Invalid artifact hash", "sha256 must be 64 lowercase hexadecimal characters.")
		}
	}
	if !timeout.IsUnknown() && !timeout.IsNull() && (timeout.ValueInt64() < 1 || timeout.ValueInt64() > 3600) {
		diagnostics.AddError("Invalid timeout", "timeout_seconds must be between 1 and 3600.")
	}
	if !environment.IsUnknown() && !environment.IsNull() {
		for key, value := range environment.Elements() {
			if key == "" || strings.ContainsAny(key, "=\x00") || (!value.IsUnknown() && value.IsNull()) {
				diagnostics.AddError("Invalid environment", "Environment names must be nonempty without '=' or NUL; values must be non-null strings.")
			}
			if v, ok := value.(types.String); ok && !v.IsUnknown() && strings.ContainsRune(v.ValueString(), 0) {
				diagnostics.AddError("Invalid environment", "Environment values must not contain NUL.")
			}
		}
	}
	return diagnostics
}
