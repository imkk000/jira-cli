package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/imkk000/jira-cli/internal/jira"
)

const usage = `jira - read and move issues on a Jira board

Usage:
  jira boards  [-name TEXT] [-json]
  jira sprints [-board ID] [-all] [-json]
  jira issues  [-board ID] [FILTERS] [-json]
  jira sprint  [-board ID] [-sprint NAME|ID] [FILTERS] [-json]
  jira show    [-json] KEY|URL
  jira transitions [-json] KEY|URL
  jira move    [-yes] [-dry-run] [-any-sprint] KEY|URL STATUS

Filters:
  -status NAME  -assignee me|none|USER  -open  -jql CLAUSE  -limit N
  -wide also shows TYPE, PRIORITY and ASSIGNEE

Environment (also read from ~/.config/jira-cli/config.env, then ./.env;
later files override earlier ones and real environment variables win):
  JIRA_URL        base URL, e.g. https://your-domain.atlassian.net
  JIRA_EMAIL      account email (Jira Cloud); leave unset to send JIRA_API_TOKEN as a bearer PAT (Server/Data Center)
  JIRA_API_TOKEN  API token or personal access token
  JIRA_SITE_URL   site used for browser links; detected automatically for api.atlassian.com URLs
  JIRA_BOARD_ID   default board ID
  JIRA_SPRINT     default sprint name or ID for "jira sprint" (otherwise the active sprint);
                  "jira move" only changes issues in this sprint unless -any-sprint is given

Run "jira COMMAND -h" for command flags.
`

const dotEnvFile = ".env"

type config struct {
	baseURL string
	email   string
	token   string
	siteURL string
	boardID int
	sprint  string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdin, os.Stdout)
	stop()
	if err != nil {
		log.SetFlags(0)
		log.Fatalf("jira: %v", err)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(`missing command, run "jira help"`)
	}
	cmd, rest := args[0], args[1:]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		_, err := io.WriteString(out, usage)
		return err
	}

	userConfig, err := userConfigFile(os.Getenv)
	if err != nil {
		return err
	}
	dotEnv, err := readDotEnvs(userConfig, dotEnvFile)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(envLookup(dotEnv))
	if err != nil {
		return err
	}
	client, err := jira.NewClient(cfg.baseURL, cfg.email, cfg.token)
	if err != nil {
		return err
	}
	if cfg.siteURL != "" {
		if err := client.UseSiteURL(cfg.siteURL); err != nil {
			return fmt.Errorf("JIRA_SITE_URL: %w", err)
		}
	}

	err = runCommand(ctx, cmd, client, cfg, rest, in, out)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func runCommand(ctx context.Context, cmd string, client *jira.Client, cfg config, args []string, in io.Reader, out io.Writer) error {
	switch cmd {
	case "boards":
		return runBoards(ctx, client, args, out)
	case "sprints":
		return runSprints(ctx, client, cfg, args, out)
	case "issues":
		return runIssues(ctx, client, cfg, args, out)
	case "sprint":
		return runSprint(ctx, client, cfg, args, out)
	case "show":
		return runShow(ctx, client, args, out)
	case "transitions":
		return runTransitions(ctx, client, args, out)
	case "move":
		return runMove(ctx, client, cfg, args, in, out)
	default:
		return fmt.Errorf(`unknown command %q, run "jira help"`, cmd)
	}
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		baseURL: getenv("JIRA_URL"),
		email:   getenv("JIRA_EMAIL"),
		token:   getenv("JIRA_API_TOKEN"),
		siteURL: getenv("JIRA_SITE_URL"),
		sprint:  getenv("JIRA_SPRINT"),
	}
	if cfg.baseURL == "" {
		return config{}, errors.New("JIRA_URL is not set")
	}
	if cfg.token == "" {
		return config{}, errors.New("JIRA_API_TOKEN is not set")
	}
	if v := getenv("JIRA_BOARD_ID"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil {
			return config{}, fmt.Errorf("JIRA_BOARD_ID: %w", err)
		}
		cfg.boardID = id
	}
	return cfg, nil
}
