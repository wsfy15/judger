package docker_executor

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"judger"
	"judger/errors"
	"judger/executor"
	"judger/utils"
	"judger/verifier"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultCompileContainerName = "golang:1.15"
	DefaultRunnerContainerName  = "alpine:latest"
	//DEBUG = true
	DefaultChannelSize = 100
)

var ResourcePath string

func init() {
	// 存放评测相关文件的目录，例如mock目录
	ResourcePath = os.Getenv("Resource")
}

type compileTask struct {
	*judger.Task
}

type runTask struct {
	*judger.Task
	Executable     string
	InputDirName   string
	InputFileName  string
	OutputDirName  string
	OutputFileName string
}

var _ executor.Executor = (*DockerExecutor)(nil)

type DockerExecutor struct {
	resultCh chan<- judger.Result
	taskCh   <-chan *judger.Task
	exitCh   <-chan struct{}

	runTaskCh     chan runTask
	compileTaskCh chan compileTask
	verifyTaskCh  chan *judger.Task

	verifier verifier.Verifier

	cli                   *client.Client
	compilerContainerName string
	compilerContainerID   string
	runnerContainerName   string
	quit                  bool
	enableCompile         bool
}

func (d *DockerExecutor) SetVerifier(v verifier.Verifier) error {
	d.verifier = v
	return nil
}

func (d *DockerExecutor) SetCompileConcurrency(n int) error {
	for i := 0; i < n; i++ {
		go d.Compile()
	}
	return nil
}

func (d *DockerExecutor) SetRunConcurrency(n int) error {
	for i := 0; i < n; i++ {
		go d.Run()
	}
	return nil
}

func (d *DockerExecutor) SetVerifyConcurrency(n int) error {
	for i := 0; i < n; i++ {
		go d.Verify()
	}
	return nil
}

func (d *DockerExecutor) SetCompilerContainer(image string) error {
	d.compilerContainerName = image
	return nil
}

func (d *DockerExecutor) SetRunnerContainer(image string) error {
	d.runnerContainerName = image
	return nil
}

func (d *DockerExecutor) SetResultChan(resultCh chan<- judger.Result) error {
	d.resultCh = resultCh
	return nil
}

func (d *DockerExecutor) SetTaskChan(taskCh <-chan *judger.Task) error {
	d.taskCh = taskCh
	return nil
}

func (d *DockerExecutor) SetExitChan(exitCh <-chan struct{}) error {
	d.exitCh = exitCh
	return nil
}

func (d *DockerExecutor) EnableCompiler() error {
	resp, err := d.cli.ContainerCreate(context.Background(), &container.Config{
		Tty:       true,
		OpenStdin: true,
		Image:     d.compilerContainerName,
	}, &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s/code:/code", ResourcePath),
			fmt.Sprintf("%s/exe:/exe", ResourcePath),
		},
	}, nil, nil, "")
	if err != nil {
		return err
	}

	d.compilerContainerID = resp.ID
	err = d.cli.ContainerStart(context.Background(), resp.ID, types.ContainerStartOptions{})
	return err
}

func New(opts ...executor.Option) *DockerExecutor {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	d := &DockerExecutor{
		cli:                   cli,
		compilerContainerName: DefaultCompileContainerName,
		runnerContainerName:   DefaultRunnerContainerName,
		runTaskCh:             make(chan runTask, DefaultChannelSize),
		compileTaskCh:         make(chan compileTask, DefaultChannelSize),
		verifyTaskCh:          make(chan *judger.Task, DefaultChannelSize),
	}

	for _, opt := range opts {
		if err = opt(d); err != nil {
			log.Fatal(err)
		}
	}
	return d
}

func (d *DockerExecutor) Execute() error {
	for !d.quit {
		select {
		case <-d.exitCh:
			d.quit = true
		case task := <-d.taskCh:
			//log.Println("execute task: ", task.ID)
			switch task.Status {
			case judger.CREATED:
				d.compileTaskCh <- compileTask{
					Task: task,
				}
			case judger.COMPILED:
				inputDir, inputFile := filepath.Split(task.InputPath)
				outputDir, outputFile := filepath.Split(task.OutputPath)
				d.runTaskCh <- runTask{
					Task:           task,
					Executable:     task.ExePath,
					InputDirName:   inputDir,
					InputFileName:  inputFile,
					OutputDirName:  outputDir,
					OutputFileName: outputFile,
				}
			case judger.EXECUTED:
				d.verifyTaskCh <- task
			}
		}
	}

	return nil
}

func (d *DockerExecutor) Compile() {
	for !d.quit {
		task := <-d.compileTaskCh
		//log.Println("compile task: ", task.ID)
		exePath := strings.TrimSuffix(task.CodePath, ".go")
		err := d.compile(task.CodePath, exePath)

		if err != nil {
			d.resultCh <- judger.Result{
				ID:      task.ID,
				Success: false,
				Error:   err,
			}
			continue
		}

		inputDir, inputFile := filepath.Split(task.InputPath)
		outputDir, outputFile := filepath.Split(task.OutputPath)
		d.runTaskCh <- runTask{
			Task:           task.Task,
			Executable:     exePath,
			InputDirName:   inputDir,
			InputFileName:  inputFile,
			OutputDirName:  outputDir,
			OutputFileName: outputFile,
		}
	}
}

func (d DockerExecutor) compile(input, output string) error {
	resp, err := d.cli.ContainerExecCreate(context.Background(), d.compilerContainerID, types.ExecConfig{
		// disable optimize and inline   -gcflags '-N -l'
		//Cmd:          []string{"sh", "-c", "go", "build", "-o", fmt.Sprintf("/exe/%s", output), fmt.Sprintf("/code/%s", input)},
		Cmd:          []string{"sh", "-c",
			fmt.Sprintf("go build -o /exe/%s /code/%s", output, input)},
		AttachStderr: true,
		AttachStdout: true,
	})
	if err != nil {
		return err
	}

	response, err := d.cli.ContainerExecAttach(context.Background(), resp.ID, types.ExecStartCheck{})
	if err != nil {
		return err
	}
	defer response.Close()

	commandOutput, err := utils.ReadFromBIO(response.Reader)
	if err != nil {
		return err
	}

	inspect, err := d.cli.ContainerExecInspect(context.Background(), resp.ID)
	if err != nil {
		return err
	}
	if inspect.ExitCode != 0 {
		return errors.New(errors.CE, commandOutput)
	}

	return nil
}

func (d *DockerExecutor) Run() {
	for !d.quit {
		task := <-d.runTaskCh
		//log.Println("run task: ", task.ID)
		err := d.run(task)
		//log.Println("run task finish: ", task.ID, err)
		if err != nil {
			d.resultCh <- judger.Result{
				ID:      task.ID,
				Success: false,
				Error:   err,
			}
			continue
		}

		task.Task.Status = judger.EXECUTED
		d.verifyTaskCh <- task.Task
	}
}

func (d *DockerExecutor) run(task runTask) error {
	resp, err := d.cli.ContainerCreate(context.Background(), &container.Config{
		// echo $(tr "\n" " " < /input/1.go) | timeout 2.5 /exe > /output/1.txt
		Cmd: []string{"sh", "-c",
			fmt.Sprintf("echo $(tr \"\\n\" \" \" < /input/%s) | timeout %v /exe > /output/%s",
				task.InputFileName, strconv.FormatFloat(task.Timeout, 'f', 4, 32), task.OutputFileName),
		},
		//Cmd: []string{"sh", "-c", "while true; do sleep 100; done"}, // for debug
		Image:        d.runnerContainerName,
		AttachStdout: true,
		AttachStderr: true,
	}, &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s/exe/%s:/exe:ro", ResourcePath, task.Executable),
			fmt.Sprintf("%s/output/%s:/output", ResourcePath, task.OutputDirName),
			fmt.Sprintf("%s/input/%s:/input:ro", ResourcePath, task.InputDirName),
		},
		//AutoRemove: true,
		Resources: container.Resources{
			Memory:    task.Memory,
			CPUPeriod: task.CpuPeriod,
			CPUQuota:  task.CpuQuota,
		},
	}, nil, nil, "")
	if err != nil {
		log.Println(err)
		return err
	}

	hijackedResponse, err := d.cli.ContainerAttach(context.Background(), resp.ID, types.ContainerAttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		log.Println(err)
	}

	if err = d.cli.ContainerStart(context.Background(), resp.ID, types.ContainerStartOptions{}); err != nil {
		return err
	}

	status, err := d.exec(resp.ID)
	if err != nil {
		log.Println(err)
		return err
	}

	if status.Error != nil && len(status.Error.Message) > 0 {
		err = errors.New(errors.ENV, status.Error.Message)
	} else {
		var msg string
		msg, err = utils.ReadFromBIO(hijackedResponse.Reader)
		if err != nil {
			return err
		}

		if status.StatusCode == 0 {
			return nil
		}

		if v, ok := errors.ExitedCode2JudgerError[status.StatusCode]; ok {
			err = errors.New(v, msg)
		} else {
			err = errors.New(errors.UNKNOWN, msg)
		}
	}
	//inspect, err := d.cli.ContainerInspect(context.Background(), resp.ID)
	//log.Println(inspect)
	return err
}

func (d *DockerExecutor) Verify() {
	for !d.quit {
		task := <-d.verifyTaskCh
		log.Println("verify task: ", task.ID)
		_, err := d.verifier.Verify(fmt.Sprintf("%s/output/%s", ResourcePath, task.OutputPath),
		 fmt.Sprintf("%s/answer/%s", ResourcePath, task.AnswerPath))

		d.resultCh <- judger.Result{
			ID:      task.ID,
			Success: err == nil,
			Error:   err,
		}
	}
}

func (d DockerExecutor) exec(id string) (container.ContainerWaitOKBody, error) {
	statusCh, errCh := d.cli.ContainerWait(context.Background(), id, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		return container.ContainerWaitOKBody{}, err
	case status := <-statusCh:
		return status, nil
	}
}
