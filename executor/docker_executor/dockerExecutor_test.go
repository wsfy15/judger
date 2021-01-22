package docker_executor

import (
	"context"
	"fmt"
	"io"
	"judger"
	"judger/executor"
	"judger/verifier"
	"log"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func TestDockerExecutor_Run(t *testing.T) {
	taskCh, resultCh, exitCh := make(chan *judger.Task), make(chan judger.Result), make(chan struct{})
	var options = []executor.Option{
		executor.EnableCompiler(),
		executor.WithResultChan(resultCh),
		executor.WithTaskChan(taskCh),
		executor.WithCompileConcurrency(3),
		executor.WithRunConcurrency(3),
		executor.WithExitChan(exitCh),
		executor.WithVerifyConcurrency(3),
		executor.WithVerifier(verifier.StandardVerifier{}),
	}

	go func() {
		var n = 5
		var tasks []judger.Task
		for i := 0; i < n; i++ {
			tasks = append(tasks, judger.Task{
				ID:         i,
				AnswerPath: "1.txt",
				InputPath:  "1.txt",
				OutputPath: fmt.Sprintf("1//%v.txt", i),
				CpuPeriod:  100000,
				CpuQuota:   50000,
				Timeout:    1.0,
				Memory:     8 << 20, // 8 MB
				Status:     judger.CREATED,
			})
		}

		//tasks[0].CodePath = "success.go"
		// 由于judger运行在windows环境，docker 运行在wsl中，使用filepath.Join 生成的path不适用于linux环境
		tasks[0].CodePath = "1//success.go" // filepath.Join("1", "success.go")
		tasks[1].CodePath = "out_of_bound.go"
		//tasks[2].CodePath = "rm.go"
		tasks[2].CodePath = "oom.go"
		tasks[3].CodePath = "ce.go"
		tasks[4].CodePath = "timeout.go"

		for i := 0; i < n; i++ {
			log.Println("put task: ", i)
			taskCh <- &tasks[i]
		}

		for i := 0; i < n; i++ {
			res := <-resultCh
			log.Println(res)
		}

		exitCh <- struct{}{}
	}()

	dockerExecutor := New(options...)
	if err := dockerExecutor.Execute(); err != nil {
		log.Println(err)
	}
}

func TestDockerExecutor_Exec(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		panic(err)
	}

	for _, container := range containers {
		fmt.Printf("%s %s\n", container.ID[:10], container.Image)
	}
}

func TestDockerExecutor_PullImage(t *testing.T) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatal(err)
	}

	reader, err := cli.ImagePull(context.Background(), "golang:1.15", types.ImagePullOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer reader.Close()
	io.Copy(os.Stdout, reader)
}

func TestDockerExecutor_Compile(t *testing.T) {
	dockerExecutor := New(executor.EnableCompiler())
	err := dockerExecutor.compile("rm.go", "rm")
	if err != nil {
		log.Fatal(err)
	}
}

func TestDockerExecutor_RunVerifier(t *testing.T) {
	var v verifier.StandardVerifier
	var outputs []string = []string{
		fmt.Sprintf("%v\\mock\\output\\success.txt", ResourcePath),
		fmt.Sprintf("%v\\mock\\output\\oob.txt", ResourcePath), // empty
		fmt.Sprintf("%v\\mock\\output\\1.txt", ResourcePath),   // output more
		fmt.Sprintf("%v\\mock\\output\\2.txt", ResourcePath),   // output less
	}

	for _, output := range outputs {
		cases, err := v.Verify(output,
			fmt.Sprintf("%v\\mock\\standard_output\\1.txt", ResourcePath))
		log.Println(fmt.Sprintf("%v pass %v cases with err: %v", output, cases, err))
	}
}

func TestSprintf(t *testing.T) {
	var output = "output"
	var input = "input"
	log.Println(fmt.Sprintf("build -o /exe/%s /code/%s", output, input))

	strs := []string{"sh", "-c",
		fmt.Sprintf("\" timeout %v /exe < /input/%s > /output/%s \"",
			strconv.FormatFloat(2.5, 'f', 4, 32), "1.go", "1.txt")}
	fmt.Println(strings.Join(strs, " "))
}
