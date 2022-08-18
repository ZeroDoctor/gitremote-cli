package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/alitto/pond"
)

type Project struct {
	ID            int    `json:"id" db:"id"`
	Name          string `json:"name" db:"name"`
	Description   string `json:"description" db:"description"`
	DefaultBranch string `json:"default_branch" db:"default_branch"`
	Files         []File
}

type File struct {
	ID        string `json:"id" db:"id"`
	Name      string `json:"name" db:"name"`
	Path      string `json:"path" db:"path"`
	Content   string `json:"content" db:"content"`
	ProjectID int    `json:"project_id" db:"project_id"`
}

func GetGroupProjects() ([]Project, error) {
	fmt.Println("getting group projects from gitlab...")
	var result []Project

	client := &http.Client{}
	url := os.Getenv("GITLAB_ENDPOINT") + "/groups/" + os.Getenv("GITLAB_GROUP") + "/projects?simple=true&per_page=100"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return result, fmt.Errorf("failed to make group project request " + err.Error())
	}
	req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("failed to get response from group project " + err.Error())
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("failed to get read group project body - %s", err.Error())
	}
	resp.Body.Close()

	err = json.Unmarshal(data, &result)
	if err != nil {
		return result, fmt.Errorf("failed to marshal data from group project - %s", err.Error())
	}

	more, err := GetPage(resp, url)
	if err != nil {
		fmt.Println("WARN: failed to page ", url, " [error="+err.Error()+"]")
	}

	if more != nil {
		for _, d := range more {
			var project Project
			err := json.Unmarshal(d, &project)
			if err != nil {
				fmt.Printf("WARN: failed to unmarshal more projects [error=%s]\n", err.Error())
				continue
			}

			result = append(result, project)
		}
	}

	var newProjects []Project
	p := pond.New(3, 0)
	projectChan := make(chan Project, 12)
	var wg sync.WaitGroup

	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		for p := range projectChan {
			newProjects = append(newProjects, p)
		}
		wg.Done()
	}(&wg)

	for i := range result {
		n := i

		p.Submit(func() {
			files, err := GetFilesFromProject(result[n].ID, result[n].DefaultBranch)
			if err != nil {
				fmt.Printf("WARN: failed to get file from project [error=%s]\n", err.Error())
				return
			}

			result[n].Files = files
			projectChan <- result[n]
		})

	}

	p.StopAndWait()
	close(projectChan)
	wg.Wait()

	return newProjects, err
}

func GetFilesFromProject(id int, branch string) ([]File, error) {
	fmt.Println("getting files from project", id)
	var result []File

	client := &http.Client{}
	url := os.Getenv("GITLAB_ENDPOINT") + "/projects/" + strconv.Itoa(id) + "/repository/tree?recursive=true&per_page=100"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return result, fmt.Errorf("failed to make group project request " + err.Error())
	}
	req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("failed to get response from group project " + err.Error())
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("failed to get read group project body - %s", err.Error())
	}
	resp.Body.Close()

	err = json.Unmarshal(data, &result)
	if err != nil {
		return result, fmt.Errorf("failed to unmarshal response - %s [data=%s]", err.Error(), string(data))
	}

	more, err := GetPage(resp, url)
	if err != nil {
		return result, fmt.Errorf("failed to get more pages - %s", err.Error())
	}

	for _, d := range more {
		var files []File
		err = json.Unmarshal(d, &files)
		if err != nil {
			fmt.Printf("WARN: failed to unmarshal more pages [error=%s] [data=%s]\n", err.Error(), string(d))
			continue
		}

		result = append(result, files...)
	}

	var newFile []File
	p := pond.New(3, 0)
	fileChan := make(chan File, 30)
	var wg sync.WaitGroup

	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		for f := range fileChan {
			newFile = append(newFile, f)
		}
		wg.Done()
	}(&wg)

	for i := range result {
		n := i
		p.Submit(func() {
			temp, err := GetContentFromFile(id, branch, result[n])
			if err != nil {
				fmt.Printf("WARN: failed to get content from pages [error=%s]\n", err.Error())
				return
			}

			result[n].ProjectID = id
			result[n].Content = temp.Content
			fileChan <- result[n]
		})
	}

	p.StopAndWait()
	close(fileChan)
	wg.Wait()

	return newFile, nil
}

func GetContentFromFile(projectID int, branch string, page File) (File, error) {
	if AvoidFiles(page.Path) {
		return page, nil
	}

	fmt.Println("getting content from file", page.Path)
	var result File

	filePath := strings.ReplaceAll(page.Path, "/", "%2F")
	filePath = strings.ReplaceAll(filePath, ".", "%2E")

	client := &http.Client{}
	url := os.Getenv("GITLAB_ENDPOINT") + "/projects/" + strconv.Itoa(projectID) + "/repository/files/" + filePath + "?ref=" + branch
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return result, fmt.Errorf("failed to make group project request [path=%s] [error=%s]", page.Path, err.Error())
	}
	req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("failed to get response from group project [path=%s] [error=%s]", page.Path, err.Error())
	}

	if resp.StatusCode > 299 || resp.StatusCode < 200 {
		fmt.Printf("WARN: failed to get [path=%s] [status_code=%d]\n", page.Path, resp.StatusCode)
		return page, nil
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("failed to get read group project body [path=%s] [error=%s]", page.Path, err.Error())
	}
	resp.Body.Close()

	err = json.Unmarshal(data, &result)
	if err != nil {
		return result, fmt.Errorf("failed to unmarshal response [path=%s] [error=%s] [data=%s]", page.Path, err.Error(), string(data))
	}

	return result, nil
}

func GetPage(resp *http.Response, url string) ([][]byte, error) {
	var result [][]byte

	totalPageStr := resp.Header.Get("x-total-pages")
	if totalPageStr == "" {
		return nil, nil
	}

	totalPage, err := strconv.Atoi(totalPageStr)
	if err != nil {
		return result, fmt.Errorf("failed to convert string [error=%s]", err.Error())
	}

	var errs []error
	p := pond.New(3, 0)

	errChan := make(chan error, 20)
	resultChan := make(chan []byte, 30)
	done := make(chan struct{}, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		for {
			select {
			case err := <-errChan:
				if err == nil {
					continue
				}

				errs = append(errs, err)
			case r := <-resultChan:
				result = append(result, r)
			case <-done:
				return
			}
		}
	}(&wg)

	for i := 2; i < totalPage; i++ {
		n := i

		p.Submit(func() {
			client := &http.Client{}
			req, err := http.NewRequest("GET", url+"&page="+strconv.Itoa(n), nil)
			if err != nil {
				errChan <- fmt.Errorf("failed to make request [error=%s]", err.Error())
				return
			}
			req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

			resp, err := client.Do(req)
			if err != nil {
				errChan <- fmt.Errorf("failed to get response [error=%s]", err.Error())
				return
			}

			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				errChan <- fmt.Errorf("failed to get read body [error=%s]", err.Error())
				return
			}
			resp.Body.Close()

			resultChan <- data
		})
	}

	p.StopAndWait()
	close(errChan)
	close(resultChan)
	done <- struct{}{}
	wg.Wait()

	if len(errs) > 0 {
		var errStr strings.Builder
		errStr.WriteString("ERROR: [errors=\n")

		for _, e := range errs {
			errStr.WriteString(e.Error())
		}

		return result, fmt.Errorf("%s", errStr.String()+"\n]")
	}

	return result, nil
}

func AvoidFiles(path string) bool {
	if !strings.Contains(path, ".") || // must have an extension
		strings.Contains(path, ".git/") || // avoid .git files
		strings.Contains(path, "package-lock.json") || // avoid package-lock.json files
		strings.Contains(path, ".js.map") || // avoid js map files
		strings.Contains(path, ".min.") || // avoid mini files
		strings.Contains(path, ".png") || // avoid images
		strings.Contains(path, ".jpeg") || // avoid images
		strings.Contains(path, ".jpg") || // avoid images
		strings.Contains(path, ".webp") || // avoid images
		strings.Contains(path, ".ico") || // avoid images
		strings.Contains(path, ".pdf") || // avoid pdf
		strings.Contains(path, ".icc") || // avoid color profiles
		strings.Contains(path, ".image") || // avoid docker images
		strings.Contains(path, ".db") || // avoid sqlite database
		strings.Contains(path, ".exe") { // avoid windows binary files
		return true
	}

	return false
}
