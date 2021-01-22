# 评测机
## 结构
- executor: 将评测分为编译、运行、校验答案 三个阶段
  - 需要设置环境变量`Resource`，值为存放`code、input、output、exe、answer`等资源的父目录，例如`mock`目录的路径
  - 支持同时运行多个goroutine执行compile、run、verify工作，具体使用参考`executor\docker_executor\dockerExecutor_test.go`的`TestDockerExecutor_Run`
  - 目前实现通过Docker执行每个阶段的工作，未来可以增加K8s或其他环境
- verifier: 比较标准答案和程序输出，保证这些文件都是相同编码，同样的换行(LF)
- errors: 评测相关的错误，包括编译、运行、校验等过程产生的问题


## 异常情况
- TLE: exited code: 143 SIGTERM terminated by timeout
- RE: exited code: 2
  - OOM
  - Index out of bound
- Killed: 
- 容器被删除:  exited code: 137
- 恶意系统调用: 
  - 删除文件: 以只读方式挂载可执行文件和输入目录，输出目录由于只挂载该用户的目录，即使删除（以及`/bin`等目录）也不会影响到其他人。
  - ...

### TODO
- 异常情况下的`Msg`还需要处理


## 缺点
- 需要运行所有测试用例，无法在某个用例出现问题时提前中止
- 每个题目只能有一个输入文件，因为运行可执行文件的容器只会执行一次命令，所以所有用例都存储在同一个文件中
