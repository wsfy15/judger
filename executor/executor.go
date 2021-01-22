package executor

import (
	"judger"
	"judger/verifier"
)

type Executor interface {
	SetCompilerContainer(image string) error

	SetRunnerContainer(image string) error

	SetVerifier(v verifier.Verifier) error

	SetResultChan(resultCh chan<- judger.Result) error

	SetTaskChan(taskCh <-chan *judger.Task) error

	SetExitChan(exitCh <-chan struct{}) error

	SetCompileConcurrency(n int) error

	SetRunConcurrency(n int) error

	SetVerifyConcurrency(n int) error

	// 编译为二进制文件
	//Compile(input, output string) error

	EnableCompiler() error

	// 运行二进制文件
	Execute() error
}
