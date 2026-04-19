package tools

func errResult(msg string) Result {
	return Result{
		Output:  msg,
		IsError: true,
	}
}
