// Package {{ .Args.Package }} contains the types for schema '{{ schema .Args.Schema }}'.
package {{ .Args.Package }}

{{ range .Templates }}
   {{ .Buf }}
{{ end }}
