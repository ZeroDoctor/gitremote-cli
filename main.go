package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v2"
)

func CacheProjects(projects []Project) {
	fmt.Println("caching projects...")
	for _, project := range projects {
		err := DB().InsertProject(project)
		if err != nil {
			fmt.Printf("WARN: failed to insert project into sqlite [error=%s]\n", err.Error())
		}
	}
	fmt.Println("finished caching projects")
}

func GetProjects(projects []string, fromCache bool) []Project {
	if !fromCache {
		return UpdateCache()
	}

	if len(projects) > 0 {
		fmt.Printf("selecting from [projects=%v]...", projects)

		result, err := DB().SelectProjects(projects)
		if err != nil {
			log.Fatalf("failed to query saved [projects=%v] [error=%s]", projects, err.Error())
		}

		return result
	}

	fmt.Println("selecting all projects...")
	result, err := DB().SelectAllProjects()
	if err != nil {
		log.Fatalf("failed to query all saved projects [error=%s]", err.Error())
	}

	return result
}

type Found struct {
	content    string
	lineNumber int
}

func FindExpression(pattern, fileContent string, context int) ([]Found, error) {
	var result []Found

	lines := strings.Split(fileContent, "\n")
	for i, line := range lines {
		found, err := regexp.Match(pattern, []byte(line))
		if err != nil {
			return nil, err
		}

		if found {
			str := "\x1b[1;36m" + line + "\x1b[0m"
			minContext := i - context
			if minContext < 0 {
				minContext = 0
			}

			maxContext := i + context
			if maxContext >= len(lines) {
				maxContext = len(lines) - 1
			}

			var builder strings.Builder
			for j := minContext; j < i; j++ {
				builder.WriteString(lines[j] + "\n")
			}
			builder.WriteString(str + "\n")
			for j := maxContext; j > i; j-- {
				builder.WriteString(lines[j] + "\n")
			}

			result = append(result, Found{content: builder.String() + "\n", lineNumber: i + 1})
		}
	}

	return result, nil
}

func UpdateCache() []Project {
	result, err := GetGroupProjects()
	if err != nil {
		log.Fatalf("failed to get groups projects [error=%s]", err.Error())
	}

	CacheProjects(result)

	return result
}

func Action(ctx *cli.Context) error {
	if ctx.Bool("update") {
		UpdateCache()
		fmt.Println("done!")
		return nil
	}

	if ctx.Args().Len() <= 0 {
		return fmt.Errorf("ERROR: expected regex")
	}

	regex := ctx.Args().Get(0)
	context := ctx.Int("context")
	if context <= 0 {
		context = 1
	}

	fmt.Println("grabing projects...")
	projects := GetProjects(ctx.StringSlice("projects"), ctx.Bool("cache"))

	fmt.Printf("looking for [pattern=%s]\n", regex)
	for _, project := range projects {
		fmt.Printf("checking project [name=%s]\n", project.Name)
		for _, file := range project.Files {
			data, err := base64.StdEncoding.DecodeString(file.Content)
			if err != nil {
				return fmt.Errorf("ERROR: failed to decode content [error=%s]", err.Error())
			}

			found, err := FindExpression(regex, string(data), context)
			if err != nil {
				return fmt.Errorf("ERROR: failed to use [regex=%s] [error=%s]\n", regex, err.Error())
			}

			if found != nil {
				for _, f := range found {
					fmt.Printf("Found in [project=%s] [file=%s] [line=%d]\n[pattern=\n%s]\n\n", project.Name, file.Path, f.lineNumber, f.content)
				}
			}
		}
	}

	fmt.Println("done!")

	return nil
}

func main() {
	if err := godotenv.Load(".env"); err != nil {
		log.Fatalf("failed to load .env file [error=%s]", err.Error())
	}

	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.BoolFlag{
			Name:    "cache",
			Aliases: []string{"c"},
			Usage:   "if used, program will only use current cache projects",
		},

		&cli.BoolFlag{
			Name:    "update",
			Aliases: []string{"u"},
			Usage:   "update projects in cache",
		},

		&cli.StringSliceFlag{
			Name:    "projects",
			Aliases: []string{"p"},
			Usage:   "specifiy which projects to use the expression",
		},

		&cli.IntFlag{
			Name:    "context",
			Aliases: []string{"C"},
			Usage:   "shows lines above and below the matched expression",
		},
	}
	app.Action = Action

	sort.Sort(cli.FlagsByName(app.Flags))
	sort.Sort(cli.CommandsByName(app.Commands))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		err := app.RunContext(ctx, os.Args)
		if err != nil {
			log.Fatalf("failed to run cli [error=%s]", err.Error())
		}
		cancel()
	}()

	OnExitWithContext(ctx, func(s os.Signal, i ...interface{}) {
		cancel()
	})

}
