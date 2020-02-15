package main

//go:generate ./tpl.sh
//go:generate ./gen.sh models

import (
	"database/sql"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"text/template"

	"github.com/eyesore/xo/internal"
	"github.com/eyesore/xo/models"
	"github.com/go-openapi/inflect"
	"github.com/xo/dburl"

	arg "github.com/alexflint/go-arg"

	_ "github.com/eyesore/xo/loaders"
	_ "github.com/xo/xoutil"
)

func main() {
	// circumvent all logic to just determine if xo was built with oracle
	// support
	if len(os.Args) == 2 && os.Args[1] == "--has-oracle-support" {
		var out int
		if _, ok := internal.SchemaLoaders["ora"]; ok {
			out = 1
		}

		fmt.Fprintf(os.Stdout, "%d", out)
		return
	}

	var err error

	// get defaults
	internal.Args = internal.NewDefaultArgs()
	args := internal.Args

	// parse args
	arg.MustParse(args)

	// process args
	err = processArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// open database
	err = openDB(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer args.DB.Close()

	// load schema name
	if args.Schema == "" {
		args.Schema, err = args.Loader.SchemaName(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	// load defs into type map
	if args.QueryMode {
		err = args.Loader.ParseQuery(args)
	} else {
		err = args.Loader.LoadSchema(args)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// add xo
	err = args.ExecuteTemplate(internal.XOTemplate, "xo_db", "", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// output
	err = writeTypes(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// processArgs processs cli args.
func processArgs(args *internal.ArgType) error {
	var err error

	// get working directory
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	// determine out path
	if args.Out == "" {
		args.Path = cwd
	} else {
		// determine what to do with Out
		fi, err := os.Stat(args.Out)
		if err == nil && fi.IsDir() {
			// out is directory
			args.Path = args.Out
		} else if err == nil && !fi.IsDir() {
			// file exists (will truncate later)
			args.Path = path.Dir(args.Out)
			args.Filename = path.Base(args.Out)

			// error if not split was set, but destination is not a directory
			if !args.SingleFile {
				return errors.New("output path is not directory")
			}
		} else if _, ok := err.(*os.PathError); ok {
			// path error (ie, file doesn't exist yet)
			args.Path = path.Dir(args.Out)
			args.Filename = path.Base(args.Out)

			// error if split was set, but dest doesn't exist
			if !args.SingleFile {
				return errors.New("output path must be a directory and already exist when not writing to a single file")
			}
		} else {
			return err
		}
	}

	// check user template path
	if args.TemplatePath != "" {
		fi, err := os.Stat(args.TemplatePath)
		if err == nil && !fi.IsDir() {
			return errors.New("template path is not directory")
		} else if err != nil {
			return errors.New("template path must exist")
		}
	}

	// fix path
	if args.Path == "." {
		args.Path = cwd
	}

	// determine package name
	if args.Package == "" {
		args.Package = path.Base(args.Path)
	}

	// determine filename if not previously set
	if args.Filename == "" {
		args.Filename = args.Package + args.Suffix
	}

	// if query mode toggled, but no query, read Stdin.
	if args.QueryMode && args.Query == "" {
		buf, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		args.Query = string(buf)
	}

	// query mode parsing
	if args.Query != "" {
		args.QueryMode = true
	}

	// check that query type was specified
	if args.QueryMode && args.QueryType == "" {
		return errors.New("query type must be supplied for query parsing mode")
	}

	// query trim
	if args.QueryMode && args.QueryTrim {
		args.Query = strings.TrimSpace(args.Query)
	}

	// escape all
	if args.EscapeAll {
		args.EscapeSchemaName = true
		args.EscapeTableNames = true
		args.EscapeColumnNames = true
	}

	// if verbose
	if args.Verbose {
		models.XOLog = func(s string, p ...interface{}) {
			fmt.Printf("SQL:\n%s\nPARAMS:\n%v\n\n", s, p)
		}
	}

	return nil
}

// openDB attempts to open a database connection.
func openDB(args *internal.ArgType) error {
	var err error

	// parse dsn
	u, err := dburl.Parse(args.DSN)
	if err != nil {
		return err
	}

	// save driver type
	args.LoaderType = u.Driver

	// grab loader
	var ok bool
	args.Loader, ok = internal.SchemaLoaders[u.Driver]
	if !ok {
		return errors.New("unsupported database type")
	}

	// open database connection
	args.DB, err = sql.Open(u.Driver, u.DSN)
	if err != nil {
		return err
	}

	return nil
}

// getFile calls  os.OpenFile on filename with the correct parameters depending on the state of args.
func getFile(args *internal.ArgType, filename string) (*os.File, error) {
	var f *os.File
	var err error

	// default open mode
	mode := os.O_RDWR | os.O_CREATE | os.O_TRUNC
	if !args.Overwrite {
		mode = mode | os.O_EXCL
	}

	// stat file to determine if file already exists
	fi, err := os.Stat(filename)
	if err == nil && fi.IsDir() {
		return nil, errors.New("filename cannot be directory")
	} else if _, ok := err.(*os.PathError); !ok && args.Append {
		// file exists so append if append is set and not XO type
		mode = os.O_APPEND | os.O_WRONLY
	}

	// open file
	f, err = os.OpenFile(filename, mode, 0666)
	if err != nil {
		return nil, err
	}

	return f, nil
}

func goimports(fname string) error {
	output, err := exec.Command("goimports", "-w", fname).CombinedOutput()
	if err != nil {
		// TODO better logging of err
		log.Println("goimports error:", err)
		return errors.New(string(output))
	}

	return nil
}

func writeTypes(args *internal.ArgType) error {
	for _, tName := range internal.TableTemplateKeys {
		tableTemplate := internal.GetTableTemplate(tName)

		masterTemplate, err := tableTemplate.AssociateTemplate(internal.XOTable, tableTemplate)
		if err != nil {
			return err
		}

		if !args.SingleFile {
			filename := args.GetFilePath(inflect.Underscore(tableTemplate.T.Name()))
			// write out table template
			f, err := getFile(args, filename)
			if err != nil {
				if os.IsExist(err) {
					log.Println("not overwriting existing file: ", filename)
					continue
				}
				return err
			}
			err = masterTemplate.Execute(f, tableTemplate)
			f.Close() // TODO check error?
			if err != nil {
				return err
			}

			if !args.SkipGoImports {
				err = goimports(filename)
				if err != nil {
					return err
				}
			}

			continue
		}
		// execute the tt into its own buffer
		err = masterTemplate.Execute(tableTemplate.Buf, tableTemplate)
		if err != nil {
			return err
		}
	}
	if args.SingleFile {
		// execute the single file template against all previously generated buffers
		obj := internal.GetSingleFileData()

		tName := internal.XOSingleFile.String()
		sfName := tName + ".go.tpl"
		b, err := args.TemplateLoader(sfName)
		if err != nil {
			return err
		}

		masterTemplate, err := template.New(tName).Funcs(internal.Args.NewTemplateFuncs()).Parse(string(b))
		if err != nil {
			return err
		}
		filename := args.GetFilePath("") // name will be overwritten anyway
		f, err := getFile(args, filename)
		if err != nil {
			return err
		}

		err = masterTemplate.Execute(f, obj)
		f.Close() // TODO same error check
		if err != nil {
			return err
		}

		if !args.SkipGoImports {
			err = goimports(filename)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
