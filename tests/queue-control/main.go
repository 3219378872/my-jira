// queue-control supports the isolated container acceptance scenario. It is not
// part of any deployed application image and never defaults to a business queue.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/hibiken/asynq"
)

func main() {
	if os.Getenv("QUEUE_CHECK_REDIS_ADDR") != "127.0.0.1:26380" || os.Getenv("QUEUE_CHECK_COMPOSE_PROJECT") != "my-jira-bootstrap-test" {
		panic("queue acceptance requires the dedicated my-jira-bootstrap-test Redis on 127.0.0.1:26380")
	}
	if len(os.Args) < 2 {
		panic("supply pause, resume, status, task <task-id>, or replay <task-id>")
	}
	options := asynq.RedisClientOpt{Addr: os.Getenv("QUEUE_CHECK_REDIS_ADDR")}
	inspector := asynq.NewInspector(options)
	defer inspector.Close()
	var err error
	switch os.Args[1] {
	case "pause":
		err = inspector.PauseQueue("default")
	case "resume":
		err = inspector.UnpauseQueue("default")
	case "replay", "task":
		if len(os.Args) != 3 {
			panic("replay needs a task identifier")
		}
		var task *asynq.TaskInfo
		task, err = inspector.GetTaskInfo("default", os.Args[2])
		if err == nil {
			if os.Args[1] == "task" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"state": task.State.String(), "retried": task.Retried})
				break
			}
			if task.Type != "export.generate" {
				panic("acceptance replay is limited to its export task")
			}
			client := asynq.NewClient(options)
			defer client.Close()
			_, err = client.Enqueue(asynq.NewTask(task.Type, task.Payload), asynq.TaskID(task.ID+"-acceptance-replay"), asynq.Retention(time.Hour))
		}
	case "status":
		var info *asynq.QueueInfo
		info, err = inspector.GetQueueInfo("default")
		if err == nil {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"pending": info.Pending, "active": info.Active, "retry": info.Retry, "completed": info.Completed, "paused": info.Paused})
		}
	default:
		err = fmt.Errorf("unknown queue acceptance action")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
