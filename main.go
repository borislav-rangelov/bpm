package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const dependencyFilename = "bpm.json"
const vendorFolderName = "vendor"
const gitFolderName = ".git"

func main() {
	ex, _ := os.Executable()

	initCommand := flag.NewFlagSet("init", flag.ExitOnError)
	installCommand := flag.NewFlagSet("install", flag.ExitOnError)
	rebuildCommand := flag.NewFlagSet("rebuild", flag.ExitOnError)

	if len(os.Args) == 1 {
		showHelp()
		return
	}

	switch os.Args[1] {
	case "init":
		initCommand.Parse(os.Args[2:])
	case "install":
		installCommand.Parse(os.Args[2:])
	case "rebuild":
		rebuildCommand.Parse(os.Args[2:])
	default:
		fmt.Printf("%q is not a valid command.\n", os.Args[1])
		os.Exit(2)
	}

	var (
		init    = initCommand.Parsed()
		install = installCommand.Parsed()
		rebuild = rebuildCommand.Parsed()
		dir     = flag.String("dir", "", "Root dir of project. Would pull all dependencies in $dir/vendor.")
	)

	flag.Parse()

	if init {
		doInit(filepath.Dir(ex))
		return
	}

	if *dir == "" {
		dir = findPackageFile(filepath.Dir(ex))
		if dir == nil {
			log.Panicf("No git repository found in folder or parent folders.\n")
		}
	}

	log.Printf("Working dir: %s\n", *dir)

	if install {
		doInstall(*dir)
	} else if rebuild {
		doRebuild(*dir)
	} else {
		showHelp()
	}
}

func showHelp() {
	fmt.Println("Basic Package Manager")
	fmt.Println("=====================")
	fmt.Print("Usage: bpm <command> [<args>]\n\n")
	fmt.Println("Commands:")
	fmt.Println("    init       Creates a bpm.json file in the current directory and gets all dependencies.")
	fmt.Println("    install    Pulls configured packages and version.")
	fmt.Print("    rebuild    Forgets all dependency data and pulls latest package versions.\n\n")
	fmt.Println("Args:")
	fmt.Println("    -dir       Root dir of project. Would pull all dependencies in $dir/vendor.")
}

func findPackageFile(dir string) *string {
	for dir != "." {
		if fileExists(filepath.Join(dir, dependencyFilename)) {
			return &dir
		}
		nextDir, _ := filepath.Abs(dir + "/..")
		if dir == nextDir {
			break
		}
		dir = nextDir
	}
	return nil
}

func doInit(dir string) {
	depFile := filepath.Join(dir, dependencyFilename)
	if fileExists(depFile) {
		fmt.Printf("%s already exists: %s", dependencyFilename, depFile)
		return
	}
}

func doInstall(dir string) {

}

func doRebuild(dir string) {
	vendorDir := filepath.Join(dir, vendorFolderName)
	removeDir(vendorDir)
	files := getAllSourceFiles(dir)
	log.Printf("Found files: %d", len(*files))
	imports := getAllImports(files)
	packages := getImports(imports)
	dependencies := installPackages(packages, dir)
	data := bpmEntry{Dependencies: dependencies}
	writeDataFile(&data)
}

func getAllImports(files *[]string) []*ast.ImportSpec {
	var (
		bytes   []byte
		err     error
		f       *ast.File
		imports = []*ast.ImportSpec{}
	)
	for _, fname := range *files {
		if bytes, err = ioutil.ReadFile(fname); err != nil {
			log.Panic(err)
		}

		fs := token.NewFileSet()
		if f, err = parser.ParseFile(fs, "", string(bytes), parser.ImportsOnly); err != nil {
			log.Panic(err)
		}

		imports = append(imports, f.Imports...)
	}
	return imports
}

func getAllSourceFiles(dir string) *[]string {
	result := []string{}

	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Panic(err)
	}

	for _, f := range files {
		fullName := filepath.Join(dir, f.Name())
		if f.IsDir() {
			if f.Name() == vendorFolderName {
				log.Printf("Skipping vendor folder: %s\n", fullName)
				continue
			}
			result = append(result, *getAllSourceFiles(fullName)...)
			continue
		}
		if strings.HasSuffix(fullName, ".go") {
			log.Printf("File: %s\n", fullName)
			result = append(result, fullName)
		}
	}

	return &result
}

func getImports(arr []*ast.ImportSpec) []*string {
	pattern, err := regexp.Compile("[^/]+\\.[^.]{1,6}/[^/]+/[^/]+")
	if err != nil {
		log.Panic(err)
	}

	imports := make(map[string]*interface{}, 0)

	for _, i := range arr {
		val := (*i.Path).Value
		if pattern.MatchString(val) {
			if _, ok := imports[val]; !ok {
				log.Printf("Found package: %s", val)
				imports[val] = nil
			}
		}
	}

	result := make([]*string, len(imports))
	for key := range imports {
		key = strings.Trim(key, `"`)
		result = append(result, &key)
	}
	return result
}

type bpmEntry struct {
	URL          string               `json:"url,omitempty"`
	Branch       string               `json:"branch,omitempty"`
	Commit       string               `json:"commit,omitempty"`
	Dependencies map[string]*bpmEntry `json:"dependencies"`
}

func installPackages(packages []*string, dir string) map[string]*bpmEntry {
	vendorDir := filepath.Join(dir, vendorFolderName)
	createDir(vendorDir)

	dependencies := make(map[string]*bpmEntry, len(packages))

	for _, f := range packages {
		if f == nil {
			continue
		}

		pkgDir := filepath.Join(vendorDir, filepath.FromSlash(*f))
		cloneURL := "https://" + *f

		log.Printf("Pulling package %s in %s...", cloneURL, pkgDir)
		log.Println(cloneRepo(cloneURL, pkgDir))

		branch := getCurrentBranch(pkgDir)
		hash := getCurrentCommitHash(pkgDir)

		dependencies[*f] = &bpmEntry{
			URL:    cloneURL,
			Branch: branch,
			Commit: hash}
	}

	return dependencies
}

func removeDir(dir string) {
	if fileExists(dir) {
		err := os.RemoveAll(dir)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func createDir(dir string) {
	if !fileExists(dir) {
		err := os.Mkdir(dir, os.ModePerm)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return !os.IsNotExist(err)
}

func runCmd(dir *string, command string, args ...string) []byte {
	var (
		out []byte
		err error
	)
	cmd := exec.Command(command, args...)
	if dir != nil {
		cmd.Dir = *dir
	}
	if out, err = cmd.CombinedOutput(); err != nil {
		log.Panic(err)
	}
	return out
}

func cloneRepo(url string, dir string) string {
	return string(runCmd(nil, "git", "clone", url, dir))
}

func getCurrentBranch(dir string) string {
	out := runCmd(&dir, "git", "branch")
	branch := string(regexp.MustCompile("\\* ([^\n]+)\n").Find(out))
	branch = strings.TrimLeft(branch, "* ")
	branch = strings.TrimRight(branch, "\n ")
	return branch
}

func getCurrentCommitHash(dir string) string {
	hash := string(runCmd(&dir, "git", "rev-parse", "HEAD"))
	hash = strings.TrimRight(hash, "\n ")
	return hash
}

func jsonEncodeIndented(deps *bpmEntry) []byte {
	buffer := bytes.Buffer{}
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(deps); err != nil {
		log.Panic(err)
	}
	return buffer.Bytes()
}

func writeDataFile(data *bpmEntry) {
	if err := ioutil.WriteFile(dependencyFilename, jsonEncodeIndented(data), os.ModeExclusive); err != nil {
		log.Panic(err)
	}
}

func isGitRepo(dir string) bool {
	return fileExists(filepath.Join(dir, gitFolderName))
}
