package verifier

import (
	"bufio"
	"fmt"
	"io"
	"judger/errors"
	"os"
	"strings"
)

type Verifier interface {
	// 返回通过了多少个case，及是否出错
	Verify(outputFileName, answerFileName string) (int, error)
}

type StandardVerifier struct{}

// TODO 优化错误处理
func (StandardVerifier) Verify(outputFileName, answerFileName string) (cases int, err error) {
	outputFd, err := os.Open(outputFileName)
	if err != nil {
		return 0, errors.New(errors.OutputNotFound, fmt.Sprintf("%v not found", outputFileName))
	}
	defer outputFd.Close()
	outputReader := bufio.NewReader(outputFd)

	answerFd, err := os.Open(answerFileName)
	if err != nil {
		return 0, errors.New(errors.AnswerNotFound, fmt.Sprintf("%v not found", answerFileName))
	}
	defer answerFd.Close()
	answerReader := bufio.NewReader(answerFd)

	for {
		answer, err := answerReader.ReadString('\n')
		output, anotherErr := outputReader.ReadString('\n')
		if err != nil || anotherErr != nil {
			if err == io.EOF && anotherErr == io.EOF {
				return cases, nil
			}
			if err != nil {
				return cases, fmt.Errorf("answer file error: %v", err)
			}
			return cases, fmt.Errorf("output file error: %v", anotherErr)
		}

		if strings.Compare(answer, output) != 0 {
			return cases, fmt.Errorf("wrong answer at %v case", cases)
		}

		cases++
	}
}
