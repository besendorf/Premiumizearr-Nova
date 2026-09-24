package stringqueue

import "sync"

func NewStringQueue() *StringQueue {
	return &StringQueue{queue: make([]string, 0), inFlight: make(map[string]struct{}), mutex: &sync.Mutex{}}
}

func (UploadQueue *StringQueue) Len() int {
	UploadQueue.mutex.Lock()
	defer UploadQueue.mutex.Unlock()
	return len(UploadQueue.queue)
}

func (UploadQueue *StringQueue) Add(path string) {
	UploadQueue.mutex.Lock()
	defer UploadQueue.mutex.Unlock()
	UploadQueue.queue = append(UploadQueue.queue, path)
}

// AddUnique adds path unless it is already waiting or being processed.
func (UploadQueue *StringQueue) AddUnique(path string) bool {
	UploadQueue.mutex.Lock()
	defer UploadQueue.mutex.Unlock()

	if _, processing := UploadQueue.inFlight[path]; processing {
		return false
	}

	for _, queuedPath := range UploadQueue.queue {
		if queuedPath == path {
			return false
		}
	}

	UploadQueue.queue = append(UploadQueue.queue, path)
	return true
}

func (UploadQueue *StringQueue) PopTopOfQueue() (bool, string) {
	UploadQueue.mutex.Lock()
	defer UploadQueue.mutex.Unlock()
	if len(UploadQueue.queue) > 0 {
		rtn := UploadQueue.queue[0]
		UploadQueue.queue = UploadQueue.queue[1:]
		UploadQueue.inFlight[rtn] = struct{}{}
		return true, rtn
	}
	return false, ""
}

// Done releases a path after the worker has finished processing it.
func (UploadQueue *StringQueue) Done(path string) {
	UploadQueue.mutex.Lock()
	defer UploadQueue.mutex.Unlock()
	delete(UploadQueue.inFlight, path)
}

func (UploadQueue *StringQueue) GetQueue() []string {
	UploadQueue.mutex.Lock()
	defer UploadQueue.mutex.Unlock()
	return append([]string(nil), UploadQueue.queue...)
}
