// Package naming compiles and renders section 11.8's path templates: a
// template is data, not code, so a user edits one in the interface and sees
// exactly what it produces before anything on disk changes. Compilation is
// where a bad template fails -- an unknown token or an unclosed brace is a
// compile error, never a runtime "{Unknown Token}" in a filename.
//
// Tokens are declared by the caller (ADR-23: an extension point, not a
// switch in core) via Context, so this package knows nothing about music,
// TV or any other media type's own vocabulary.
package naming
