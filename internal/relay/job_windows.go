//go:build windows

package relay

import (
	"syscall"
	"unsafe"
)

// 让 ffmpeg 子进程跟随本进程退出。
//
// 光靠 relay.Stop() 不够：应用被任务管理器结束、崩溃、或者 Ctrl+C 没走完
// 清理时，Go 根本没机会 Kill 子进程。Windows 不像 Linux 那样有进程组连坐，
// 父进程一死，ffmpeg 就成了孤儿——继续拉流、继续转码，残留几个就能把 CPU
// 吃满，之后所有播放都卡，而且用户完全看不到它们。
//
// 解法是 Job Object：给 ffmpeg 建一个 Job，设 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE，
// 把进程塞进去。本进程结束时内核关掉最后一个 Job 句柄，里面的进程被连带
// 结束——不管我们是怎么死的。
var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procOpenProcess              = kernel32.NewProc("OpenProcess")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

// jobBasicLimitInformation 对应 JOBOBJECT_BASIC_LIMIT_INFORMATION。
// 字段宽度必须和 C 一致；Go 的对齐填充正好补上 DWORD 后面那段空隙，
// 所以这里不写显式的 _ 填充字段，否则会多出 4 字节。
type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

// jobIOCounters 对应 IO_COUNTERS：六个 uint64。
type jobIOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// jobExtendedLimitInformation 对应 JOBOBJECT_EXTENDED_LIMIT_INFORMATION。
type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IOInfo                jobIOCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// bindProcessLifecycle 把 pid 绑到本进程的生死上，返回的函数用于主动释放
// Job 句柄。不调用也能在进程退出时自动收尾，只是正常 Stop 之后句柄会多留
// 到进程结束。
//
// 失败不算致命：部分环境（沙箱、某些容器）不允许把进程塞进新 Job，
// 调用方记一条 debug 日志继续跑就行，行为退回到「只有 Stop 能清理」。
func bindProcessLifecycle(pid int) (func(), error) {
	job, _, err := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return nil, winErr(err)
	}

	info := jobExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	if r, _, err := procSetInformationJobObject.Call(
		job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info),
	); r == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return nil, winErr(err)
	}

	proc, _, err := procOpenProcess.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if proc == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return nil, winErr(err)
	}
	defer syscall.CloseHandle(syscall.Handle(proc))

	if r, _, err := procAssignProcessToJobObject.Call(job, proc); r == 0 {
		syscall.CloseHandle(syscall.Handle(job))
		return nil, winErr(err)
	}
	return func() { syscall.CloseHandle(syscall.Handle(job)) }, nil
}

// winErr 把 LazyProc.Call 的错误兜成非 nil：成功路径上它是 Errno(0)。
func winErr(err error) error {
	if err == nil {
		return syscall.EINVAL
	}
	return err
}
