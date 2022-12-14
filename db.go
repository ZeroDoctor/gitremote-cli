package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/zerodoctor/gitremote-cli/gitlab"
)

var dbHandler *DBHandler
var once sync.Once

func DB() *DBHandler {
	once.Do(func() {
		dbHandler = connect()
	})

	return dbHandler
}

type DBHandler struct {
	lite *sqlx.DB
}

func connect() *DBHandler {
	path, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to get [file=%s] [error=%s]\n", path, err.Error())
	}

	index := strings.LastIndex(path, "/")
	if index == -1 {
		index = strings.LastIndex(path, "\\")
	}

	path = path[:index]

	lite, err := sqlx.Connect("sqlite3", path+"/lite.db")
	if err != nil {
		log.Fatalf("failed to connect to [path=%s] [error=%s]", path+"/lite.db", err.Error())
	}

	db := &DBHandler{lite: lite}
	err = db.CreateLiteTables()
	if err != nil {
		log.Fatalf("failed to create sqlite tables [error=%s]", err.Error())
	}

	return db
}

type SchemaColumn struct {
	TableName  string `db:"table_name"`
	DataType   string `db:"data_type"`
	ColumnName string `db:"column_name"`
}

func (db *DBHandler) CreateLiteTables() error {
	table := `CREATE TABLE IF NOT EXISTS projects (
	  id INTEGER NOT NULL PRIMARY KEY,
	  name TEXT NOT NULL,
	  description TEXT,
	  default_branch TEXT
	);`

	_, err := db.lite.Exec(table)
	if err != nil {
		return err
	}

	table = `CREATE TABLE IF NOT EXISTS files (
	  id TEXT NOT NULL PRIMARY KEY,
	  name TEXT NOT NULL,
	  path TEXT NOT NULL,
	  content TEXT,
	  project_id INTEGER NOT NULL,
	  FOREIGN KEY(project_id) REFERENCES projects(id)
	);`

	_, err = db.lite.Exec(table)

	return err
}

func (db *DBHandler) InsertProject(project gitlab.Project) error {
	insert := "INSERT OR REPLACE INTO projects (id, name, description, default_branch) VALUES (:id, :name, :description, :default_branch);"

	_, err := db.lite.NamedExec(insert, project)
	if err != nil {
		return err
	}

	for _, file := range project.Files {
		err = db.InsertFile(file)
		if err != nil {
			fmt.Printf("WARN: failed to insert file into sqlite [error=%s]\n", err.Error())
			continue
		}
	}

	return nil
}

func (db *DBHandler) InsertFile(file gitlab.File) error {
	insert := "INSERT OR REPLACE INTO files (id, name, path, content, project_id) VALUES (:id, :name, :path, :content, :project_id);"
	_, err := db.lite.NamedExec(insert, file)
	return err
}

func (db *DBHandler) SelectProjects(projectNames []string) ([]gitlab.Project, error) {
	var projects []gitlab.Project

	query, args, err := sqlx.In("SELECT * FROM projects WHERE name IN (?);", projectNames)
	if err != nil {
		return projects, err
	}
	query = db.lite.Rebind(query)

	err = db.lite.Select(&projects, query, args...)
	if err != nil {
		return projects, err
	}

	for i, project := range projects {
		files, err := db.SelectFiles(project.ID)
		if err != nil {
			fmt.Printf("WARN: failed to get all failes [error=%s]\n", err.Error())
			continue
		}

		projects[i].Files = files
	}

	return projects, nil
}

func (db *DBHandler) SelectAllProjects() ([]gitlab.Project, error) {
	var projects []gitlab.Project

	query := "SELECT * FROM projects;"

	err := db.lite.Select(&projects, query)
	if err != nil {
		return projects, err
	}

	for i, project := range projects {
		files, err := db.SelectFiles(project.ID)
		if err != nil {
			fmt.Printf("WARN: failed to get all files [error=%s]\n", err.Error())
			continue
		}

		projects[i].Files = files
	}

	return projects, nil
}

func (db *DBHandler) SelectFiles(projectID int) ([]gitlab.File, error) {
	var files []gitlab.File
	err := db.lite.Select(&files, "SELECT * FROM files WHERE project_id = $1", projectID)
	return files, err
}
