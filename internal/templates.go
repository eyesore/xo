package internal

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"text/template"

	templates "github.com/eyesore/xo/tplbin"
)

var (
	// TableTemplateKeys preservers the order of template creation.  This only matters for args.SingleFile == true
	TableTemplateKeys = []string{}

	// use a map for easy access to the templates - order is preserved in TableTemplateKeys
	tableTemplates = map[string]*TableTemplate{}
)

// TemplateLoader loads templates from the specified name.
func (a *ArgType) TemplateLoader(name string) ([]byte, error) {
	if a.TemplatePath != "" {
		tplPath := path.Join(a.TemplatePath, name)
		_, err := os.Stat(tplPath)
		if !os.IsNotExist(err) {
			// if the template is there, load it, else fall back to asset
			return ioutil.ReadFile(path.Join(a.TemplatePath, name))
		}
	}

	return templates.Asset(name)
}

// TemplateSet retrieves the created template set.
func (a *ArgType) TemplateSet() *TemplateSet {
	if a.templateSet == nil {
		a.templateSet = &TemplateSet{
			funcs: a.NewTemplateFuncs(),
			l:     a.TemplateLoader,
			tpls:  map[string]*template.Template{},
		}
	}

	return a.templateSet
}

// GetTemplateName constructs the name of the template to pass to the loader.  Note that this will no longer be used as T.Name.
func (a *ArgType) GetTemplateName(tt TemplateType) string {
	loaderType := ""
	if tt != XOTemplate && tt != XOTable {
		if a.LoaderType == "oci8" || a.LoaderType == "ora" {
			// force oracle for oci8 since the oracle driver doesn't recognize
			// 'oracle' as valid protocol
			loaderType = "oracle."
		} else {
			loaderType = a.LoaderType + "."
		}
	}

	return fmt.Sprintf("%s%s.go.tpl", loaderType, tt)
}

// ExecuteTemplate loads and parses the supplied template with name and
// executes it with obj as the context.
func (a *ArgType) ExecuteTemplate(tt TemplateType, name string, sub string, obj interface{}) error {
	var err error

	// setup generated
	if a.Generated == nil {
		a.Generated = []TBuf{}
	}

	// create store
	v := TBuf{
		TemplateType: tt,
		Name:         name,
		Subname:      sub,
		Buf:          new(bytes.Buffer),
	}

	templateName := a.GetTemplateName(tt)

	// execute template
	err = a.TemplateSet().Execute(v.Buf, templateName, obj)
	if err != nil {
		return err
	}

	a.Generated = append(a.Generated, v)
	return nil
}

// TemplateSet is a set of templates.
type TemplateSet struct {
	funcs template.FuncMap
	l     func(string) ([]byte, error)
	tpls  map[string]*template.Template
}

// Execute executes a specified template in the template set using the supplied
// obj as its parameters and writing the output to w.
func (ts *TemplateSet) Execute(w io.Writer, name string, obj interface{}) error {
	tpl, ok := ts.tpls[name]
	if !ok {
		// attempt to load and parse the template
		buf, err := ts.l(name)
		if err != nil {
			return err
		}

		// parse template
		tpl, err = template.New(name).Funcs(ts.funcs).Parse(string(buf))
		if err != nil {
			return err
		}
	}

	return tpl.Execute(w, obj)
}

// TableTemplate holds the template and associated metadata for a single table's output
type TableTemplate struct {
	// T holds all defined associated templates for this table. T.Name should be tablename
	T *template.Template

	// Args gives us access to the package name, template loader, etc.  TODO move this out of args
	Args *ArgType

	// Dots maps the component template names to the data objects to execute against
	Dots map[string][]interface{}

	// Buf should only be used for single file, I think
	Buf *bytes.Buffer
}

// GetTableTemplate returns the TableTemplate that contains the named template, or creates it
func GetTableTemplate(name string) *TableTemplate {
	if _, ok := tableTemplates[name]; !ok {
		tt := &TableTemplate{
			T:    template.New(name),
			Args: Args,
			Dots: map[string][]interface{}{},
			Buf:  new(bytes.Buffer),
		}
		tableTemplates[name] = tt
		TableTemplateKeys = append(TableTemplateKeys, name)
	}

	return tableTemplates[name]
}

// AssociateTemplate loads the template for tt and associates it with the TableTemplate.
// It will eventually be executed against obj
func (t *TableTemplate) AssociateTemplate(tt TemplateType, obj interface{}) (*template.Template, error) {
	loaderName := t.Args.GetTemplateName(tt)
	b, err := t.Args.TemplateLoader(loaderName)
	if err != nil {
		return nil, err
	}
	templateName := tt.String()
	out, err := t.T.New(templateName).Funcs(t.Args.NewTemplateFuncs()).Parse(string(b))
	if err != nil {
		return nil, err
	}

	if _, ok := t.Dots[templateName]; !ok {
		t.Dots[templateName] = make([]interface{}, 1)
	}
	t.Dots[templateName] = append(t.Dots[templateName], obj)

	return out, nil
}

// GetSingleFileData returns the data for the single file template to be executed against.  Type does not matter.
func GetSingleFileData() interface{} {
	return struct {
		Args      *ArgType
		Templates map[string]*TableTemplate
	}{Args, tableTemplates}
}
