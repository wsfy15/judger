package docker_executor

import (
	"context"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
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
	"sync"
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
	sync.Mutex

	cli                    *client.Client // docker client
	compilerContainerImage string
	compilerContainerID    string
	runnerContainerImage   string
	quit                   bool
	enableCompile          bool
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
	d.compilerContainerImage = image
	return nil
}

func (d *DockerExecutor) SetRunnerContainer(image string) error {
	d.runnerContainerImage = image
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
	return d.startCompiler()
}

func New(opts ...executor.Option) *DockerExecutor {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	d := &DockerExecutor{
		cli:                    cli,
		compilerContainerImage: DefaultCompileContainerName,
		runnerContainerImage:   DefaultRunnerContainerName,
		runTaskCh:              make(chan runTask, DefaultChannelSize),
		compileTaskCh:          make(chan compileTask, DefaultChannelSize),
		verifyTaskCh:           make(chan *judger.Task, DefaultChannelSize),
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
		case task := <-d.taskCh: // 接收外部传入的任务，并根据任务状态执行
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
		// 可执行文件相对exe目录的路径 与 源代码文件相对code目录的路径 相同
		task.ExePath = strings.TrimSuffix(task.CodePath, ".go")
		err, rerun := d.compile(task)

		if err != nil {
			d.resultCh <- judger.Result{
				ID:      task.ID,
				Success: false,
				Error:   err,
			}
			continue
		}

		if rerun {
			continue
		}

		inputDir, inputFile := filepath.Split(task.InputPath)
		outputDir, outputFile := filepath.Split(task.OutputPath)
		d.runTaskCh <- runTask{
			Task:           task.Task,
			InputDirName:   inputDir,
			InputFileName:  inputFile,
			OutputDirName:  outputDir,
			OutputFileName: outputFile,
		}
	}
}

func (d *DockerExecutor) compile(task compileTask) (err error, rerun bool) {
	defer func() {
		if rerun, err = d.checkCompilerError(err); err != nil {
			return
		} else if rerun {
			d.compileTaskCh <- task
		}
	}()

	input, output := task.CodePath, task.ExePath
	// 保证目录存在
	outputDir := filepath.Dir(output)
	if outputDir != "." {
		utils.CheckDirectoryExist(fmt.Sprintf("%s/exe/%s", ResourcePath, outputDir))
	}

	resp, err := d.cli.ContainerExecCreate(context.Background(), d.compilerContainerID, types.ExecConfig{
		// disable optimize and inline   -gcflags '-N -l'
		Cmd: []string{"sh", "-c",
			fmt.Sprintf("go build -o /exe/%s /code/%s", output, input)},
		AttachStderr: true,
		AttachStdout: true,
	})
	if err != nil {
		return
	}

	response, err := d.cli.ContainerExecAttach(context.Background(), resp.ID, types.ExecStartCheck{})
	if err != nil {
		return
	}
	defer response.Close()

	err = d.cli.ContainerExecStart(context.Background(), resp.ID, types.ExecStartCheck{})
	if err != nil {
		return
	}

	commandOutput, err := utils.ReadFromBIO(response.Reader)
	if err != nil {
		return
	}

	inspect, err := d.cli.ContainerExecInspect(context.Background(), resp.ID)
	if err != nil {
		return
	}
	if inspect.ExitCode != 0 {
		return errors.New(errors.CE, commandOutput), false
	}

	return
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
	// 保证目录存在
	if task.OutputDirName != "." {
		utils.CheckDirectoryExist(fmt.Sprintf("%s/output/%s", ResourcePath, task.OutputDirName))
	}

	resp, err := d.cli.ContainerCreate(context.Background(), &container.Config{
		// echo $(tr "\n" " " < /input/1.go) | timeout 2.5 /exe > /output/1.txt
		Cmd: []string{"sh", "-c",
			fmt.Sprintf("echo $(tr \"\\n\" \" \" < /input/%s) | timeout %v /exe > /output/%s",
				task.InputFileName, strconv.FormatFloat(task.Timeout, 'f', 4, 32), task.OutputFileName),
		},
		//Cmd: []string{"sh", "-c", "while true; do sleep 100; done"}, // for debug
		Image:        d.runnerContainerImage,
		AttachStdout: true,
		AttachStderr: true,
	}, &container.HostConfig{
		Binds: []string{
			fmt.Sprintf("%s/exe/%s:/exe:ro", ResourcePath, task.ExePath),
			fmt.Sprintf("%s/output/%s:/output", ResourcePath, task.OutputDirName),
			fmt.Sprintf("%s/input/%s:/input:ro", ResourcePath, task.InputDirName),
		},
		//AutoRemove: true,
		Resources: container.Resources{
			Memory:     task.Memory,
			MemorySwap: task.Memory,
			CPUPeriod:  task.CpuPeriod,
			CPUQuota:   task.CpuQuota,
		},
	}, nil, nil, "")
	if err != nil {
		log.Println(task.ID, err)
		return err
	}

	hijackedResponse, err := d.cli.ContainerAttach(context.Background(), resp.ID, types.ContainerAttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		log.Println(task.ID, err)
	}

	if err = d.cli.ContainerStart(context.Background(), resp.ID, types.ContainerStartOptions{}); err != nil {
		log.Println(task.ID, err)
		return err
	}

	status, err := d.exec(resp.ID)
	if err != nil {
		log.Println(task.ID, err)
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
		//log.Println("verify task: ", task.ID)
		_, err := d.verifier.Verify(fmt.Sprintf("%s/output/%s", ResourcePath, task.OutputPath),
			fmt.Sprintf("%s/answer/%s", ResourcePath, task.AnswerPath))

		d.resultCh <- judger.Result{
			ID:      task.ID,
			Success: err == nil,
			Error:   err,
		}
	}
}

func (d *DockerExecutor) exec(id string) (container.ContainerWaitOKBody, error) {
	statusCh, errCh := d.cli.ContainerWait(context.Background(), id, container.WaitConditionNotRunning)

	select {
	case err := <-errCh:
		return container.ContainerWaitOKBody{}, err
	case status := <-statusCh:
		return status, nil
	}
}

// 启动一个编译容器 并记录容器ID
func (d *DockerExecutor) startCompiler() error {
	resp, err := d.cli.ContainerCreate(context.Background(), &container.Config{
		Tty:       true,
		OpenStdin: true,
		Image:     d.compilerContainerImage,
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
	return d.cli.ContainerStart(context.Background(), resp.ID, types.ContainerStartOptions{})
}

func (d *DockerExecutor) restartCompiler() error {
	d.Lock()
	defer d.Unlock()

	_, err := d.cli.ContainerInspect(context.Background(), d.compilerContainerID)
	if errdefs.IsNotFound(err) {
		return d.startCompiler()
	}
	return err
}

// if recover from error by restarting compiler, and restart success, then need to rerun
// if fail to restart compiler, don't rerun
func (d *DockerExecutor) checkCompilerError(err error) (rerun bool, e error) {
	if err == nil || errors.IsError(err, errors.CE) {
		return
	}

	log.Println("compiler error: ", err)
	if err = d.restartCompiler(); err != nil {
		return false, err
	}
	return true, nil
}
