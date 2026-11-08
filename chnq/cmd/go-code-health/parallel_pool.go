package main

import (
	"context"
	"sync"
)

// workerpool manages concurrent analysis of packages/files
type WorkerPool struct {
	workers    int
	workChan   chan *AnalysisTask
	resultChan chan *AnalysisResult
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
}

// analysistask represents work for a worker
type AnalysisTask struct {
	FilePath  string
	FileIndex int
}

// analysisresult holds the result of analyzing one file
type AnalysisResult struct {
	FileIndex  int
	FileReport *FileReport
	Error      error
}

// newworkerpool creates a new pool with specified worker count
func NewWorkerPool(workerCount int) *WorkerPool {
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > 256 {
		workerCount = 256
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkerPool{
		workers:    workerCount,
		workChan:   make(chan *AnalysisTask, workerCount*2),
		resultChan: make(chan *AnalysisResult, workerCount*2),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// start initializes the worker goroutines
func (p *WorkerPool) Start(analyzeFunc func(context.Context, string) (*FileReport, error)) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(analyzeFunc)
	}
}

// worker processes tasks from the work channel
func (p *WorkerPool) worker(analyzeFunc func(context.Context, string) (*FileReport, error)) {
	defer p.wg.Done()
	for task, ok := p.nextTask(); ok; task, ok = p.nextTask() {
		report, err := analyzeFunc(p.ctx, task.FilePath)
		p.resultChan <- &AnalysisResult{
			FileIndex:  task.FileIndex,
			FileReport: report,
			Error:      err,
		}
	}
}

// nexttask handles next task
func (p *WorkerPool) nextTask() (*AnalysisTask, bool) {
	select {
	case <-p.ctx.Done():
		return nil, false
	case task, ok := <-p.workChan:
		return task, ok
	}
}

// submit enqueues work for the pool
func (p *WorkerPool) Submit(task *AnalysisTask) {
	p.workChan <- task
}

// wait closes the work channel and waits for all workers to finish
func (p *WorkerPool) Wait() {
	close(p.workChan)
	p.wg.Wait()
	close(p.resultChan)
}

// results returns the result channel
func (p *WorkerPool) Results() <-chan *AnalysisResult {
	return p.resultChan
}

// stop cancels all workers
func (p *WorkerPool) Stop() {
	p.cancel()
	p.Wait()
}
