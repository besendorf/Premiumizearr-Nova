package stringqueue

import "testing"

func TestAddUnique(t *testing.T) {
	queue := NewStringQueue()
	if added := queue.AddUnique("movie.nzb"); !added {
		t.Fatal("first AddUnique call did not add the path")
	}
	if added := queue.AddUnique("movie.nzb"); added {
		t.Fatal("second AddUnique call added a duplicate path")
	}
	if length := queue.Len(); length != 1 {
		t.Fatalf("queue length = %d, want 1", length)
	}
}

func TestAddUniqueExcludesInFlightPath(t *testing.T) {
	queue := NewStringQueue()
	if !queue.AddUnique("movie.nzb") {
		t.Fatal("initial AddUnique did not add the path")
	}
	if ok, path := queue.PopTopOfQueue(); !ok || path != "movie.nzb" {
		t.Fatalf("PopTopOfQueue() = (%t, %q), want (true, movie.nzb)", ok, path)
	}
	if queue.AddUnique("movie.nzb") {
		t.Fatal("AddUnique added a path while it was being processed")
	}
	if length := queue.Len(); length != 0 {
		t.Fatalf("queue length while processing = %d, want 0", length)
	}

	queue.Done("movie.nzb")
	if !queue.AddUnique("movie.nzb") {
		t.Fatal("AddUnique did not allow the path after processing completed")
	}
}
