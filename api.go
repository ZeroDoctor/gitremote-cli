package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/alitto/pond"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/zerodoctor/zdtui/ui"
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
	var projects []Project

	client := &http.Client{}
	url := os.Getenv("GITLAB_ENDPOINT") + "/groups/" + os.Getenv("GITLAB_GROUP") + "/projects?simple=true&per_page=100"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return projects, fmt.Errorf("failed to make group project request " + err.Error())
	}
	req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		return projects, fmt.Errorf("failed to get response from group project " + err.Error())
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return projects, fmt.Errorf("failed to get read group project body - %s", err.Error())
	}
	resp.Body.Close()

	err = json.Unmarshal(data, &projects)
	if err != nil {
		return projects, fmt.Errorf("failed to marshal data from group project - %s", err.Error())
	}

	more, err := GetAllFileInfo(resp, url)
	if err != nil {
		fmt.Println("[WARN] failed to page ", url, " [error="+err.Error()+"]")
	}

	if more != nil {
		for _, d := range more {
			var project Project
			err := json.Unmarshal(d, &project)
			if err != nil {
				fmt.Printf("[WARN] failed to unmarshal more projects [error=%s]\n", err.Error())
				continue
			}

			projects = append(projects, project)
		}
	}

	var newProjects []Project
	projectChan := make(chan Project, 12)
	var wg sync.WaitGroup

	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		for p := range projectChan {
			newProjects = append(newProjects, p)
		}
		wg.Done()
	}(&wg)

	var program *tea.Program
	var mp *ui.MultiProgressBar
	var workload []ui.ProgressWork

	for i := range projects {
		n := i

		workload = append(workload, func(pb *ui.ProgressBar) error {
			pb.Display(fmt.Sprintf("grabbing %s...", projects[n].Name))
			files, err := GetFilesFromProject(pb, projects[n].ID, projects[n].DefaultBranch)
			if err != nil {
				pb.Display(fmt.Sprintf("[ERROR] failed to get file from project [error=%s]\n", err.Error()))
				return err
			}

			pb.Display(fmt.Sprintf("updating %s files", projects[n].Name))
			projects[n].Files = files
			projectChan <- projects[n]

			return nil
		})

		if (i+1)%3 == 0 {
			mp, _ = ui.NewMultiProgress(context.Background(), workload)

			program = tea.NewProgram(mp)
			if err := program.Start(); err != nil {
				fmt.Printf("[ERROR] failed to start program [error=%s]", err.Error())
			}

			workload = []ui.ProgressWork{}
		}
	}

	if len(workload) > 0 {
		mp, _ = ui.NewMultiProgress(context.Background(), workload)

		program = tea.NewProgram(mp)
		if err := program.Start(); err != nil {
			fmt.Printf("[ERROR] failed to start program [error=%s]", err.Error())
		}
	}

	close(projectChan)
	wg.Wait()

	return newProjects, err
}

func GetFilesFromProject(pb *ui.ProgressBar, id int, branch string) ([]File, error) {
	var files []File

	client := &http.Client{}
	url := os.Getenv("GITLAB_ENDPOINT") + "/projects/" + strconv.Itoa(id) + "/repository/tree?recursive=true&per_page=100"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return files, fmt.Errorf("failed to make group project request " + err.Error())
	}
	req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		return files, fmt.Errorf("failed to get response from group project " + err.Error())
	}
	pb.SendTick(0.1)

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return files, fmt.Errorf("failed to get read group project body - %s", err.Error())
	}
	resp.Body.Close()

	err = json.Unmarshal(data, &files)
	if err != nil {
		return files, fmt.Errorf("failed to unmarshal response - %s [data=%s]", err.Error(), string(data))
	}
	pb.SendTick(0.1)

	pb.Display("grabbing all file info...")
	more, err := GetAllFileInfo(resp, url)
	if err != nil {
		return files, fmt.Errorf("failed to get more pages - %s", err.Error())
	}

	for _, d := range more {
		var filesInfo []File
		err = json.Unmarshal(d, &filesInfo)
		if err != nil {
			pb.Display(fmt.Sprintf("[WARN] failed to unmarshal more pages [error=%s] [data=%s]\n", err.Error(), string(d)))
			continue
		}

		files = append(files, filesInfo...)
	}
	pb.SendTick(0.3)

	var filesWithContent []File
	p := pond.New(3, 0)
	fileChan := make(chan File, 30)
	var wg sync.WaitGroup

	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		for f := range fileChan {
			filesWithContent = append(filesWithContent, f)
		}
		wg.Done()
	}(&wg)

	for i := range files {
		n := i
		p.Submit(func() {
			temp, err := GetContentFromFile(pb, id, branch, files[n])
			if err != nil {
				pb.Display(fmt.Sprintf("[WARN] failed to get content from pages [error=%s]\n", err.Error()))
				return
			}

			files[n].ProjectID = id
			files[n].Content = temp.Content
			fileChan <- files[n]
		})
	}
	pb.SendTick(0.3)

	p.StopAndWait()
	close(fileChan)
	wg.Wait()

	return filesWithContent, nil
}

func GetContentFromFile(pb *ui.ProgressBar, projectID int, branch string, page File) (File, error) {
	if AvoidFiles(page.Path) {
		return page, nil
	}

	pb.Display(fmt.Sprintf("getting content from [file=%s]", page.Path))

	var file File

	filePath := strings.ReplaceAll(page.Path, "/", "%2F")
	filePath = strings.ReplaceAll(filePath, ".", "%2E")

	client := &http.Client{}
	url := os.Getenv("GITLAB_ENDPOINT") + "/projects/" + strconv.Itoa(projectID) + "/repository/files/" + filePath + "?ref=" + branch
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return file, fmt.Errorf("failed to make group project request [path=%s] [error=%s]", page.Path, err.Error())
	}
	req.Header.Add("PRIVATE-TOKEN", os.Getenv("GITLAB_TOKEN"))

	resp, err := client.Do(req)
	if err != nil {
		return file, fmt.Errorf("failed to get response from group project [path=%s] [error=%s]", page.Path, err.Error())
	}

	if resp.StatusCode > 299 || resp.StatusCode < 200 {
		pb.Display(fmt.Sprintf("[WARN] failed to get [path=%s] [status_code=%d]\n", page.Path, resp.StatusCode))
		return page, nil
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return file, fmt.Errorf("failed to get read group project body [path=%s] [error=%s]", page.Path, err.Error())
	}
	resp.Body.Close()

	err = json.Unmarshal(data, &file)
	if err != nil {
		return file, fmt.Errorf("failed to unmarshal response [path=%s] [error=%s] [data=%s]", page.Path, err.Error(), string(data))
	}

	return file, nil
}

func GetAllFileInfo(resp *http.Response, url string) ([][]byte, error) {
	var fileInfos [][]byte

	totalPageStr := resp.Header.Get("x-total-pages")
	if totalPageStr == "" {
		return nil, nil
	}

	totalPage, err := strconv.Atoi(totalPageStr)
	if err != nil {
		return fileInfos, fmt.Errorf("failed to convert string [error=%s]", err.Error())
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
				fileInfos = append(fileInfos, r)
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

		return fileInfos, fmt.Errorf("%s", errStr.String()+"\n]")
	}

	return fileInfos, nil
}

func AvoidFiles(path string) bool {
	defaultFileExtensionToAvoid := []string{
		".git/",
		"package-lock.json",
		".js.map",
		".css.map",
		".min.",
		".png",
		".jpeg",
		".jpg",
		".webp",
		".ico",
		".pdf",
		".icc",
		".image",
		".db",
		".exe",
	}

	if !strings.Contains(path, ".") { // avoid windows binary files
		return true
	}

	for i := range defaultFileExtensionToAvoid {
		if strings.Contains(path, defaultFileExtensionToAvoid[i]) {
			return true
		}
	}

	return false
}
