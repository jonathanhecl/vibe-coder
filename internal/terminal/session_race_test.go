package terminal

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// A command that exits immediately must still report what it wrote. A session
// that says "exited, no output" for a command that printed something is worse
// than a slow one: the caller cannot tell the two apart, and the whole point of
// a session is to read what the command said.
//
// The pressure is deliberate. In a quiet process the reader goroutines almost
// always reach their first Read before the child has finished forking and
// exiting, which is why this loss stayed rare enough to survive a whole suite
// run and then show up once under load.
func TestSessionKeepsOutputWhenManySessionsStartAtOnce(t *testing.T) {
	const (
		workers = 24
		rounds  = 8
	)

	var wg sync.WaitGroup
	lost := make(chan string, workers*rounds)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				manager := NewManager()
				sess, err := manager.Start("echo hello")
				if err != nil {
					lost <- fmt.Sprintf("worker %d round %d: start: %v", worker, round, err)
					continue
				}
				output, running := sess.ReadOutput(2 * time.Second)
				if !strings.Contains(output, "hello") {
					lost <- fmt.Sprintf("worker %d round %d: running=%v output=%q", worker, round, running, output)
				}
				_ = sess.Terminate()
			}
		}(worker)
	}
	wg.Wait()
	close(lost)

	var failures []string
	for line := range lost {
		failures = append(failures, line)
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d rounds lost the command's output:\n%s",
			len(failures), workers*rounds, strings.Join(failures, "\n"))
	}
}
