# Part 2A: Leader Election and heartbeat    
该部分的内容是实现领导者选举和心跳机制。Raft算法基于领导者机制，将算法本身分解为三个相关的子问题：  
1. 领导者选举   
    当现有的leader故障后，必须能及时选出一个新的leader。    
2. 日志复制     
    leader接受客户端的命令，并将命令作为日志条目(log entry)复制到自己的日志中，并发送AppendEntries RPC迫使其他所有peers同意leader日志的内容，保证所有节点日志的一致性。     
3. 安全性   
    Raft算法的核心安全属性是状态机安全属性(the State Machine Safety Property)，即如果任意的服务器已经应用一个确定的条目到它的状态机，那么其他服务器不能在同一个索引位置(the same log index)应用一个不同的指令。通过对对选举施加限制：当选的leader的日志必须是"up-to-date"，保证了领导者完整性属性(the Leader Completeness Property)。由领导者完整性属性可以反证状态机安全属性的正确性。     

这里我们分析领第一个子问题的实现思路。  

## 1. 程序结构      
Lab2的实验要求是Raft实现必须支持以下接口，测试代码和(最终的)你的key/value服务器将使用这些接口：     
```go
// create a new Raft server instance:
rf := Make(peers, me, persister, applyCh)

// start agreement on a new log entry:
rf.Start(command interface{}) (index, term, isleader)

// ask a Raft for its current term, and whether it thinks it is leader
rf.GetState() (term, isLeader)

// each time a new entry is committed to the log, each Raft peer
// should send an ApplyMsg to the service (or tester).
type ApplyMsg
```
使用Raft算法的服务，调用`Make()`接口来创建一个Raft对等点(a Raft perr)。调用`Start()`接口要求Raft启动一次处理以便将命令追加到复制日志。Raft使用课程提供的labrpc包来交换RPC，它以Go语言的rpc库为模型，但是内部使用Go channel而不是sockets。以RequestVote RPC为例，使用`sendRequestVote()`接口发送RPC，当接收到RequestVote RPC请求时，自动调用`RequestVote()`接口处理传入的RPC。根据[Lec2: Infrastructure: RPC and threads]()的讲解，我们知道Go的RPC库会创建一个新的goroutine处理传入的RequestVote请求，也就是说创建一个新的goroutine来执行`RequestVote`。所以为Raft结构注册好RPC处理函数后，在RPC请求到达时，会自动调用该处理函数。除此之外，没有更多的信息。    

### 1.1 状态机      
根据论文[extended Raft]()中图4给出的状态机转移图，我的第一个想法是将每个状态组织成一个独立的goroutine，以此为入口点，每个状态里面可能会再派生出几个goroutine，比如`Follower`状态只需要周期性检测选举超时(也就是心跳超时)，而`Candidate`在选举超时后还需要发起一次选举。     
这个结构的问题在于，在进行状态切换时，上一个状态的goroutine可能还在执行，比如选举超时goroutien这种周期性任务仍在循环执行，必须在切换到新的状态前，给上一个状态的所有goroutine发退出信号并等待它们完全退出后，再启动到新的goroutine。    
可以通过channel实现这个目的，大体思路如下： 
```go
loop:
    for {
        select {
            case <- done:
                break loop
            default:

        }
    }
```
每个gorutine的结构都是这样，切换新状态前，关闭done channel，从而使得这些goroutine退出循环并退出。   
但新的状态可能需要马上切换，比如`Leader`状态，需要立即向其他peers发送心跳，以防止其超时发起无用的选举。这是先等待上一个状态的所有goroutien结束，可能会出现问题。    
自己的第一个实现基本无法通过测试，只有偶尔可以通过第一个不存在网络故障的正常选举测试。  

### 1.2 时间驱动的(time-driven)、长期运行的(long-runing)的goroutine     
总结[状态机方案](#11-状态机)的问题：状态切换时杀掉上一个状态的goroutien同时创建新状态的goroutine，由于状态切换可能很频繁，这种做法效率低效，同时切换期间杀掉并等待上一个状态的所有goroutine退出，存在很大的风险。       
仔细分析不难发现，在这些状态的所有goroutine里，其实存在功能相同的goroutine，它们随着状态切换被频繁创建和杀掉，并且它们是长期运行的周期性任务，这样做也存在问题。正如[Raft Structure Advice](https://pdos.csail.mit.edu/6.824/labs/raft-structure.txt)所述：     
> Raft实例有两种时间驱动的(time-driven)活动：(1) 领导者必须发送心跳，(2) 以及其他(对等点)自收到领导者消息以来(since hearing from the leader)，如果太长时间过去(if too much time has passed)，开始一次选举。最好使用一个专门的(dedicated)、长期运行的(long-running)goroutine来驱动这两者中的每个活动，而不是将多个活动组合到单个goroutine中。        

可以看到，这两个时间驱动的活动涉及到两个定时任务：      
1. 心跳周期超时检测     
2. 选举超时(心跳超时)检测       
并且它们是状态互斥的，第一个是`Leader`行为，第二个是`Follower`行为(心跳超时检测)或`Candidate`行为(选举超时检测)。   
根据[Raft Structure Advice]的关于管理选举超时的建议：   
> 也许最简单的计划(plan)是在Raft结构中维护一个变量，其包含了该对等点最后一次从领导者那里听到消息的时间(the last time at which the peer heard from the leader)，并且让选举超时goroutine(the election timeout goroutine)定期进行检查，看看自那时起的时间(the time since then)是否大于超时周期。   
使用带有一个小的常量参数的`time.Sleep()`来驱动定期检查(periodic checks)是最容易的，`time.Ticker`和`time.Timer`很难正确使用。    

因为我们的程序结构包含了三个长期运行的goroutine：   
1. heartbeatPeriodTick      
2. electionTimeoutTick      
3. eventLoop    
前2个goroutine分别执行上述的两个定时检测任务，第3个goroutine用于循环检测前2个goroutine的超时channel，并执行对应的时间驱动事件。     
还有一个问题就是heartbeatPeriodTick和electionTimeoutTick是状态互斥的，也就是说对于同一个peer，任一时间要么是leader，要么不是leader，所以只能执行其中一个goroutine，而另一个goroutine由于是长期运行的，还不能退出，所以只能休眠等待，可以通过条件变量实现休眠等待，和对应状态切换时的唤醒操。    

electionTimeoutTick实现：   
```go
// 选举超时(心跳超时)检查器，定期检查自最新一次从leader那里收到AppendEntries RPC(包括heartbeat)
// 或给予candidate的RequestVote RPC请求的投票的时间(latestHeardTIme)以来的时间差，是否超过了
// 选举超时时间(electionTimeout)。若超时，则往electionTimeoutChan写入数据，以表明可以发起选举。
func (rf *Raft) electionTimeoutTick() {
	for {
		// 如果peer是leader，则不需要选举超时检查器，所以等待nonLeaderCond条件变量
		if term, isLeader := rf.GetState(); isLeader {
			rf.mu.Lock()
			rf.nonLeaderCond.Wait()
			rf.mu.Unlock()
		} else {
			rf.mu.Lock()
			elapseTime := time.Now().UnixNano() - rf.latestHeardTime
			if int(elapseTime/int64(time.Millisecond)) >= rf.electionTimeout {
				DPrintf("[ElectionTimeoutTick]: Id %d Term %d State %s\t||\ttimeout," +
						" convert to Candidate\n", rf.me, term, state2name(rf.state))
				// 选举超时，peer的状态只能是follower或candidate两种状态。
				// 若是follower需要转换为candidate发起选举； 若是candidate
				// 需要发起一次新的选举。---所以这里设置状态为Candidate---。
				// 这里不需要设置state为Candidate，因为总是要发起选举，在选举
				// 里面设置state比较合适，这样不分散。
				//rf.state = Candidate
				rf.electionTimeoutChan <- true
			}
			rf.mu.Unlock()
			// 休眠1ms，作为tick的时间间隔
			time.Sleep(time.Millisecond)
		}
	}
}
```

heartbeatPeriodTick实现与之类似。       
eventLoop实现：     
```go
// 消息处理主循环，处理两种互斥的时间驱动的时间到期：
// 1) 心跳周期到期； 2) 选举超时。
func (rf *Raft) eventLoop() {
	for {
		select {
		case <- rf.electionTimeoutChan:
			rf.mu.Lock()
			DPrintf("[EventLoop]: Id %d Term %d State %s\t||\telection timeout, start an election\n",
									rf.me, rf.currentTerm, state2name(rf.state))
			rf.mu.Unlock()
			go rf.startElection()
		case <- rf.heartbeatPeriodChan:
			rf.mu.Lock()
			DPrintf("[EventLoop]: Id %d Term %d State %s\t||\theartbeat period occurs, broadcast heartbeats\n",
								rf.me, rf.currentTerm, state2name(rf.state))
			rf.mu.Unlock()
			go rf.broadcastHeartbeat()
		}
	}
}
```

有了程序主结构后，剩下的就是实现两个对应的驱动事件：选举和广播心跳，以及对应的RequestVote RPC和AppendEntires RPC的发送和处理函数。      

## 2. 并行发送RPC组织   

