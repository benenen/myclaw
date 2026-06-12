package orchestration

import "testing"

func TestTaskStoreLifecycle(t *testing.T) {
	s := NewTaskStore()

	task := s.Create("researcher", "find X")
	if task.ID == "" || task.State != TaskStateSubmitted {
		t.Fatalf("unexpected created task: %+v", task)
	}

	s.SetWorking(task.ID)
	got, ok := s.Get(task.ID)
	if !ok || got.State != TaskStateWorking {
		t.Fatalf("expected working, got %+v ok=%v", got, ok)
	}

	s.Complete(task.ID, "the answer")
	got, _ = s.Get(task.ID)
	if got.State != TaskStateCompleted || got.Result != "the answer" {
		t.Fatalf("expected completed result, got %+v", got)
	}
}

func TestTaskStoreFailAndCancel(t *testing.T) {
	s := NewTaskStore()
	t1 := s.Create("a", "p")
	s.Fail(t1.ID, "boom")
	if got, _ := s.Get(t1.ID); got.State != TaskStateFailed || got.Error != "boom" {
		t.Fatalf("expected failed, got %+v", got)
	}

	t2 := s.Create("a", "p")
	if !s.Cancel(t2.ID) {
		t.Fatal("expected cancel to succeed for submitted task")
	}
	if got, _ := s.Get(t2.ID); got.State != TaskStateCanceled {
		t.Fatalf("expected canceled, got %+v", got)
	}
	// terminal tasks cannot be canceled
	if s.Cancel(t1.ID) {
		t.Fatal("expected cancel to fail for terminal task")
	}
}

func TestTaskStoreGetMissing(t *testing.T) {
	s := NewTaskStore()
	if _, ok := s.Get("nope"); ok {
		t.Fatal("expected missing task")
	}
}
