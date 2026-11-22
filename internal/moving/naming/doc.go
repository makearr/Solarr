// Package naming compiles and renders section 11.8's path templates: a template is data, so a bad one (unknown token, unclosed brace) fails at compile time, and tokens come from the caller via Context (ADR-23), keeping media vocabulary out of core.
package naming
