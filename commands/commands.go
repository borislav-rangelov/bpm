package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type CmdItem struct {
	cmd     *flag.FlagSet
	handler func()
	desc    string
}

type ArgItem struct {
	pVal *string
	desc string
}

type Commands struct {
	Name        string
	MainCommand string
	commands    map[string]*CmdItem
	args        map[string]*ArgItem
	nameMaxSize int
}

func (c *Commands) updateMaxSize(name string) {
	if l := len(name); c.nameMaxSize < l {
		c.nameMaxSize = l
	}
}

func (c *Commands) NewCommand(name string, handler func(), desc string) {
	cmd := flag.NewFlagSet(name, flag.ContinueOnError)
	c.AddCommand(cmd, handler, desc)
}

func (c *Commands) AddCommand(flag *flag.FlagSet, handler func(), desc string) {
	if c.commands == nil {
		c.commands = make(map[string]*CmdItem)
		c.args = make(map[string]*ArgItem)
	}
	c.commands[flag.Name()] = &CmdItem{
		cmd:     flag,
		handler: handler,
		desc:    desc}
	c.updateMaxSize(flag.Name())
}

func (c *Commands) NewArg(name string, pVal *string, def string, desc string) {
	if c.commands == nil {
		c.commands = make(map[string]*CmdItem)
		c.args = make(map[string]*ArgItem)
	}
	flag.StringVar(pVal, name, def, desc)
	c.args[name] = &ArgItem{
		pVal: pVal,
		desc: desc}
	c.updateMaxSize(name)
}

func showHelp(c *Commands) {
	sb := strings.Builder{}
	sb.WriteString(c.Name)
	sb.WriteString("\n")
	sb.WriteString("=====================\n")
	sb.WriteString("Usage: ")
	sb.WriteString(c.MainCommand)
	sb.WriteString(" <command> [<args>]\n")

	c.WriteWholeUsage(&sb)

	fmt.Print(sb.String())
	// fmt.Println("Commands:")
	// fmt.Println("    init       Creates a bpm.json file in the current directory and gets all dependencies.")
	// fmt.Println("    install    Pulls configured packages and version.")
	// fmt.Print("    rebuild    Forgets all dependency data and pulls latest package versions.\n\n")
	// fmt.Println("Args:")
	// fmt.Println("    -dir       Root dir of project. Would pull all dependencies in $dir/vendor.")
}

func HandleArgs(c *Commands) {
	if c.commands == nil {
		c.commands = make(map[string]*CmdItem)
		c.args = make(map[string]*ArgItem)
	}
	if len(os.Args) <= 1 {
		showHelp(c)
		return
	}

	cmd := os.Args[1]
	var pItem *CmdItem

	for name, item := range c.commands {
		if name == cmd {
			pItem = item
			break
		}
	}

	if pItem == nil {
		showHelp(c)
		return
	}

	flag.Parse()

	pItem.handler()
}

func (c *Commands) WriteWholeUsage(w io.Writer) {
	indent := "    "
	if len(c.commands) > 0 {
		io.WriteString(w, "Commands:\n")

		for name, item := range c.commands {
			io.WriteString(w, indent)
			io.WriteString(w, fmt.Sprintf("%-"+strconv.Itoa(c.nameMaxSize)+"s", name))
			io.WriteString(w, indent)
			io.WriteString(w, item.desc)
			io.WriteString(w, "\n")
		}
		io.WriteString(w, "\n")
	}

	if len(c.args) > 0 {
		io.WriteString(w, "Args:\n")
		for name, item := range c.args {
			io.WriteString(w, indent)
			io.WriteString(w, fmt.Sprintf("%-"+strconv.Itoa(c.nameMaxSize)+"s", name))
			io.WriteString(w, indent)
			io.WriteString(w, item.desc)
			io.WriteString(w, "\n")
		}
		io.WriteString(w, "\n")
	}
}
