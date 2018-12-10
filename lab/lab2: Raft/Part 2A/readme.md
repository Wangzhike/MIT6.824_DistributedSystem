# Part 2A: Leader Election and heartbeat    
该部分的内容是实现领导者选举和心跳机制。Raft算法基于领导者机制，将算法本身分解为三个相关的子问题：  
1. 领导者选举   
    当现有的leader故障后，必须能及时选出一个新的leader。    
2. 日志复制     
    leader接受客户端的命令，并将命令作为日志条目(log entry)复制到自己的日志中，并发送AppendEntries RPC迫使其他所有peers同意leader日志的内容，保证所有节点日志的一致性。     
3. 安全性   
    Raft算法的核心安全属性是状态机安全属性(the State Machine Safety Property)，即如果任意的服务器已经应用一个确定的条目到它的状态机，那么其他服务器不能在同一个索引位置(the same log index)应用一个不同的指令。通过对对选举施加限制：当选的leader的日志必须是"up-to-date"，保证了领导者完整性属性(the Leader Completeness Property)。由领导者完整性属性可以反证状态机安全属性的正确性。     

这里我们分析领第一个子问题的实现思路。  

1. [程序结构](#1-程序结构)
    1. [状态机](#11-状态机)
    2. [时间驱动的(time-driven)、长期运行的(long-running)的goroutine](#12-时间驱动的time-driven长期运行的long-running的goroutine)
2. [并行发送RPC结构](#2-并行发送RPC结构)
3. [RPC请求或回复中的任期超时处理](#3-RPC请求或回复中的任期超时处理)


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
使用Raft算法的服务，调用`Make()`接口来创建一个Raft对等点(a Raft perr)。调用`Start()`接口要求Raft启动一次处理以便将命令追加到复制日志。Raft使用课程提供的labrpc包来交换RPC，它以Go语言的rpc库为模型，但是内部使用Go channel而不是sockets。以RequestVote RPC为例，使用`sendRequestVote()`接口发送RPC，当接收到RequestVote RPC请求时，自动调用`RequestVote()`接口处理传入的RPC。根据[Lec2: Infrastructure: RPC and threads](https://pdos.csail.mit.edu/6.824/notes/l-rpc.txt)的讲解，我们知道Go的RPC库会创建一个新的goroutine处理传入的RequestVote请求，也就是说创建一个新的goroutine来执行`RequestVote`。所以为Raft结构注册好RPC处理函数后，在RPC请求到达时，会自动调用该处理函数。除此之外，没有更多的信息。    

### 1.1 状态机      
根据论文[extended Raft](https://raft.github.io/raft.pdf)中图4给出的状态机转移图，我的第一个想法是将每个状态组织成一个独立的goroutine，以此为入口点，每个状态里面可能会再派生出几个goroutine，比如`Follower`状态只需要周期性检测选举超时(也就是心跳超时)，而`Candidate`在选举超时后还需要发起一次选举。     
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
但新的状态可能需要马上切换，比如`Leader`状态，需要立即向其他peers发送心跳，以防止其超时发起无用的选举。这时先等待上一个状态的所有goroutien结束，可能会出现问题。    
自己的第一个实现基本无法通过测试，只有偶尔可以通过第一个不存在网络故障的正常选举测试。  

### 1.2 时间驱动的(time-driven)、长期运行的(long-running)的goroutine     
总结[状态机方案](#11-状态机)的问题：状态切换时杀掉上一个状态的goroutien同时创建新状态的goroutine，由于状态切换可能很频繁，这种做法效率低效，同时切换期间杀掉并等待上一个状态的所有goroutine退出，存在很大的风险。       
仔细分析不难发现，在这些状态的所有goroutine里，其实存在功能相同的goroutine，它们随着状态切换被频繁创建和杀掉，并且它们是长期运行的周期性任务，这样做也存在问题。正如[Raft Structure Advice](https://pdos.csail.mit.edu/6.824/labs/raft-structure.txt)所述：     
> Raft实例有两种时间驱动的(time-driven)活动：(1) 领导者必须发送心跳，(2) 以及其他(对等点)自收到领导者消息以来(since hearing from the leader)，如果太长时间过去(if too much time has passed)，开始一次选举。最好使用一个专门的(dedicated)、长期运行的(long-running)goroutine来驱动这两者中的每个活动，而不是将多个活动组合到单个goroutine中。        

可以看到，**这两个时间驱动的活动涉及到两个定时任务**：      
1. 心跳周期超时检测     
2. 选举超时(心跳超时)检测      

并且它们是状态互斥的，第一个是`Leader`行为，第二个是`Follower`行为(心跳超时检测)或`Candidate`行为(选举超时检测)。   
根据[Raft Structure Advice](https://pdos.csail.mit.edu/6.824/labs/raft-structure.txt)的关于管理选举超时的建议：   
> 也许最简单的计划(plan)是在Raft结构中维护一个变量，其包含了该对等点最后一次从领导者那里听到消息的时间(the last time at which the peer heard from the leader)，并且让选举超时goroutine(the election timeout goroutine)定期进行检查，看看自那时起的时间(the time since then)是否大于超时周期。   
使用带有一个小的常量参数的`time.Sleep()`来驱动定期检查(periodic checks)是最容易的，`time.Ticker`和`time.Timer`很难正确使用。    

因为我们的程序结构包含了三个长期运行的goroutine：   
1. `heartbeatPeriodTick`      
2. `electionTimeoutTick`     
3. `eventLoop`   

前2个goroutine分别执行上述的两个定时检测任务，第3个goroutine用于循环检测前2个goroutine的超时channel，并执行对应的时间驱动事件。     
还有一个问题就是`heartbeatPeriodTick`和`electionTimeoutTick`是状态互斥的，也就是说对于同一个peer，任一时间要么是leader，要么不是leader，所以只能执行其中一个goroutine，而另一个goroutine由于是长期运行的，还不能退出，所以只能休眠等待，可以通过条件变量实现休眠等待，和对应状态切换时的唤醒操作。    

`electionTimeoutTick`实现：   
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
            // 休眠10ms，作为tick的时间间隔。如果休眠时间太短，比如1ms，将导致频繁检查选举超时，
            // 造成测量到的user时间，即CPU时间增长，可能超过5秒。
            time.Sleep(time.Millisecond*10)
        }
    }
}
```

`heartbeatPeriodTick`实现与之类似。       
`eventLoop`实现：     
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

## 2. 并行发送RPC结构   
为了提高性能，需要并行发送RPC。可以迭代peers，为每一个peer单独创建一个goroutine发送RPC。[Raft Structure Adivce](https://pdos.csail.mit.edu/6.824/labs/raft-structure.txt)建议：     
> 在同一个goroutine里进行RPC回复(reply)处理是最简单的，而不是通过(over)channel发送回复消息。    

所以，为每个peer创建一个gorotuine同步发送RPC并进行RPC回复处理。另外，为了保证由于RPC发送阻塞而阻塞的goroutine不会阻塞RequestVote RPC的投票统计，需要在每个发送RequestVote RPC的goroutine中实时统计获得的选票数，达到多数后就立即切换为`Leader`状态，并立即发送一次心跳，阻止其他peer因选举超时而发起新的选举。而不能在等待所有发送goroutine处理结束后再统计票数，这样阻塞的goroutine，会阻塞领导者的产生。      
还有一个问题就是最好等待所有发送RPC的goroutine的退出，因为选举和广播心跳操作不能阻塞，必须立即返回。所以，为了等待发送goroutine退出，需要在并行发送RPC的外部再创建一个goroutine，用于迭代peers并行发送RPC和等待这些发送RPC的goroutine结束。     
发起选举的代码实现如下：    
```go
// 发起一次选举，在一个新的goroutine中并行给其他每个peers发送RequestVote RPC，并等待
// 所有发起RequestVote的goroutine结束。不能等所有发送RPC的goroutine结束后再统计投票，
// 选出leader，因为这样一个peer阻塞不回复RPC，就会造成无法选出leader。所以需要在发送RPC
// 的goroutine中及时统计投票结果，达到多数投票，就立即切换到leader状态。
func (rf *Raft) startElection() {
    rf.mu.Lock()
    // 再次设置下状态
    rf.switchTo(Candidate)
    // start election:
    // 	1. increase currentTerm
    rf.currentTerm += 1
    //  2. vote for self
    rf.voteFor = rf.me
    nVotes := 1
    // 	3. reset election timeout
    rf.resetElectionTimer()

    DPrintf("[StartElection]: Id %d Term %d State %s\t||\tstart an election\n",
        rf.me, rf.currentTerm, state2name(rf.state))

    rf.mu.Unlock()

    // 	4. send RequestVote RPCs to all other servers in parallel
    // 创建一个goroutine来并行给其他peers发送RequestVote RPC，由其等待并行发送RPC的goroutine结束
    go func(nVotes *int, rf *Raft) {
        var wg sync.WaitGroup
        winThreshold := len(rf.peers)/2 + 1

        for i, _ := range rf.peers {
            // 跳过发起投票的candidate本身
            if i == rf.me {
                continue
            }

            rf.mu.Lock()
            wg.Add(1)
            lastLogIndex := len(rf.log) - 1
            if lastLogIndex < 0 {
                DPrintf("[StartElection]: Id %d Term %d State %s\t||\tinvalid lastLogIndex %d\n",
                    rf.me, rf.currentTerm, state2name(rf.state), lastLogIndex)
            }
            args := RequestVoteArgs{Term: rf.currentTerm, CandidateId: rf.me,
                LastLogIndex: lastLogIndex, LastLogTerm: rf.log[lastLogIndex].Term}
            DPrintf("[StartElection]: Id %d Term %d State %s\t||\tissue RequestVote RPC"+
                " to peer %d\n", rf.me, rf.currentTerm, state2name(rf.state), i)
            rf.mu.Unlock()
            var reply RequestVoteReply

            // 使用goroutine单独给每个peer发起RequestVote RPC
            go func(i int, rf *Raft, args *RequestVoteArgs, reply *RequestVoteReply) {
                defer wg.Done()

                ok := rf.sendRequestVote(i, args, reply)

                // 发送RequestVote请求失败
                if ok == false {
                    rf.mu.Lock()
                    DPrintf("[StartElection]: Id %d Term %d State %s\t||\tsend RequestVote"+
                        " Request to peer %d failed\n", rf.me, rf.currentTerm, state2name(rf.state), i)
                    rf.mu.Unlock()
                    return
                }

                // 请求发送成功，查看RequestVote投票结果
                // 拒绝投票的原因有很多，可能是任期较小，或者log不是"up-to-date"
                if reply.VoteGranted == false {

                    rf.mu.Lock()
                    defer rf.mu.Unlock()
                    DPrintf("[StartElection]: Id %d Term %d State %s\t||\tRequestVote is"+
                        " rejected by peer %d\n", rf.me, rf.currentTerm, state2name(rf.state), i)

                    // If RPC request or response contains T > currentTerm, set currentTerm = T,
                    // convert to follower
                    if rf.currentTerm < reply.Term {
                        DPrintf("[StartElection]: Id %d Term %d State %s\t||\tless than"+
                            " peer %d Term %d\n", rf.me, rf.currentTerm, state2name(rf.state), i, reply.Term)
                        rf.currentTerm = reply.Term
                        // 作为candidate，之前投票给自己了，所以这里重置voteFor，以便可以再次投票
                        rf.voteFor = -1
                        rf.switchTo(Follower)
                    }

                } else {
                    // 获得了peer的投票
                    rf.mu.Lock()
                    DPrintf("[StartElection]: Id %d Term %d State %s\t||\tpeer %d grants vote\n",
                        rf.me, rf.currentTerm, state2name(rf.state), i)
                    *nVotes += 1
                    DPrintf("[StartElection]: Id %d Term %d State %s\t||\tnVotes %d\n",
                        rf.me, rf.currentTerm, state2name(rf.state), *nVotes)
                    // 如果已经获得了多数投票，并且是Candidate状态，则切换到leader状态
                    if rf.state == Candidate && *nVotes >= winThreshold {

                        DPrintf("[StartElection]: Id %d Term %d State %s\t||\twin election with nVotes %d\n",
                            rf.me, rf.currentTerm, state2name(rf.state), *nVotes)

                        // 切换到leader状态
                        rf.switchTo(Leader)

                        rf.leaderId = rf.me

                        // leader启动时初始化所有的nextIndex为其log的尾后位置
                        for i := 0; i < len(rf.peers); i++ {
                            rf.nextIndex[i] = len(rf.log)
                        }
                        // 不能通过写入heartbeatPeriodChan的方式表明可以发送心跳，因为
                        // 写入操作会阻塞直到eventLoop中读取该channel，而此时需要立即
                        // 发送一次心跳，以避免其他peer因超时发起无用的选举。
                        go rf.broadcastHeartbeat()
                    }
                    rf.mu.Unlock()
                }

            }(i, rf, &args, &reply)
        }

        // 等待素有发送RPC的goroutine结束
        wg.Wait()

    }(&nVotes, rf)
}
```
注意，**发起选举时，只有为`Candidate`状态且获得了多数者的投票，才能变为leader。**

## 3. RPC请求或回复中的任期超时处理     
论文[extended Raft](https://raft.github.io/raft.pdf)图2中的"Rules for Servers"中指出对于任何服务器：   
> 如果RPC请求或回复中包含的任期(term)T > currentTerm，设置currentTerm = T，并切换到`Follower`状态。     

这里需要意识到，任期过时意味着自己当前的状态失效，所以在切换到`Follower`状态时，需要根据已失效的当前状态进行一些额外的处理，比如重置`voteFor`为`null`，以便可以再次投票，以及重置选举超时计时器等。     
下面分RequestVote请求和回复，以及AppendEntiers请求处理(request handler)和回复处理(reply processing)，具体分析：    
1. RequestVote RPC  
    * 请求处理(request handler)  
        `currentTerm < args.Term`，根据当前状态：   
        * `Follower`    
            可能为正常情况，比如3个Raft实例刚启动，都处于`Follower`状态，s0的选举超时时间先耗尽，变为`Candidate`状态，任期为1发起选举。s1此时任期为0，处于`Follower`状态，收到s0的RequestVote RPC请求。这时应该继续正常执行RequestVote RPC处理程序，检查s0的日志是否"up-to-date"，如果是，则投票给s0。      
            但仍然可以将`voteFor`重置为-1，因为既然该peer的`rf.currentTerm < args.Term`，说明该peer此时还没有给哪个candidate投票，因为一旦它投过票，其任期就会更新为`args.Term`。所以此时重置`voteFor`为-1是安全的，往下继续执行处理，仍然可以投票。    
        * `Candidate`   
            说明此候选者状态过时，由于`Candidate`在发起选举时给自己投票，会将`voteFor`设置为自身的id，所以在切换到`Follower`状态时，需要重置`voteFor`为-1，以便可以再次投票。~~同时相当于了解到更高任期的候选者的信息，需要重置选举超时计时器~~。这里不需要重置选举超时计时器，该工作在接下来给出投票时进行重置，如果拒绝了投票请求，就不会重置选举超时计时器，这时它可以再次发起选举。该RequestVote请求合法，继续执行处理。  
        * `Leader`  
            一种可能的情况是，3个Raft实例，s0为leader，s1和s2为follower，任期都为1。这是s0宕机，s2由于选举超时变为candidate，发起选举，任期为2。这期间s0恢复，收到s2的RequestVote请求。由于leader在发起选举时投票给自己，s0需要重置`voteFor`为-1。~~同时重置选举超时定时器~~。该RequestVote请求合法，继续执行处理。     
            s0由leader切换到follower状态时，需要给`nonLeaderCond`条件变量发广播，以唤醒休眠的`electionTimeoutTick`goroutine。我们通过`swithTo()`函数统一处理状态切换，以便可以不遗漏的处理leader和nonLeader状态切换引起的需要给`leaderCond`或`nonLeaderCond`条件变量发信号的处理。      

        以上可以看出，在RequestVote RPC的请求处理中，当`rf.currentTerm < args.Term`时，除了设置`rf.currentTerm = args.Term`，切换为`Follower`状态外，不管该peer之前处于什么状态，都需要重置`voteFor`为-1，然后继续执行请求处理，根据args参数是否是“up-to-date”，以决定是否给出投票。        
        对于该peer之前处于`Follower`和`Candidate`的场景，再给出一个例子：比如有5个server，启动后s0, s2, s4选取的选举超时时间相同，同时超时，所以同时发起选举(s0, s2, s4发起选举前再次重置选举超时计时器)，s0获得自身和s1的投票，s2获得自身和s3的投票，s4只有自己的投票，三者都没有获得大多数选票，此term1选举被瓜分，如下图(a)所示：    
        ![RequestVote RPC handler任期超时处理](figures/RequestVote%20RPC%20handler%E5%A4%84%E7%90%86%E4%BB%BB%E6%9C%9F%E8%BF%87%E6%97%B6.png)      
        紧接着，s2率先选举超时，再次发起选举，如(b)所示，此时，s0, s4作为candidate，重置`voteFor`为-1，s1, s3作为follower，重置`voteFor`为-1，如(c)所示，然后，由于s2满足"up-to-date"，获得所有peers的投票，变为leader，如(d)所示。     

    * 回复处理(reply processing)  
        `currentTerm < reply.Term`，此时类似于请求中的`Candidate`状态，说明此候选者状态过时，进行和上面一样的处理。     
2. AppendEntires RPC    
    * 请求  
        收到任期大于自己的AppendEntires RPC请求，说明已经存在一个合法的leader。这时如果是`Follower`或`Candidate`状态，重置`voteFor`为-1，并重置选举超时定时器。如果是`Leader`状态，除了进行以上步骤外，还需要给`nonLeaderCond`条件变量发广播，以唤醒休眠的`electionTimeoutTick`goroutine，`switchTo()`调用会进行给条件变量发广播处理。  
    * 回复  
        同收到AppendEntires RPC请求的过时的leader的处理。   

**在重置选举超时定时器时，需要重新随机化选举选举超时时间`electionTimout`**。如果不这么做，如果出现若干个follower的`electionTimemout`相同，则它们同时选举超时，同时发起投票，如果它们瓜分了选票；然后选举超时再次发生，再次同时发起选举，再一次出现选票瓜分，无法选出leader。为了避免这种情况，应该每次重置选举超时计时器时都重新选取随机化的选举超时时间，以尽量避免选举超时相同的情况。    

