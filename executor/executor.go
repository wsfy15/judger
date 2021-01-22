package executor

import (
	"judger"
	"judger/verifier"
)

type Executor interface {
	// go 容器镜像，默认为 golang:1.15
	SetCompilerContainer(image string) error

	// 运行可执行文件的容器镜像，默认为 alpine:latest
	SetRunnerContainer(image string) error

	SetVerifier(v verifier.Verifier) error

	SetResultChan(resultCh chan<- judger.Result) error

	SetTaskChan(taskCh <-chan *judger.Task) error

	SetExitChan(exitCh <-chan struct{}) error

	SetCompileConcurrency(n int) error

	SetRunConcurrency(n int) error

	SetVerifyConcurrency(n int) error

	// 启动编译容器
	EnableCompiler() error

	// 运行Executor
	Execute() error
}
