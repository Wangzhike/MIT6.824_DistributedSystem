# Part 2B: Log Replication  
> 实现领导者和跟随者代码来追加新的日志条目。这将涉及到实现`Start()`，完成AppendEntries RPC结构，发送它们，充实(fleshing out)AppendEntry RPC处理程序(handler)，以及推进(advancing)领导者的`commitIndex`。    

## 1. `Start()`实现     
使用Raft的服务调用`Start()`想要就追加到Raft的log中下一条命令开始达成一致(start agreement)。如果该服务器不是leader，它返回false，否则开始达成一致并立即返回。不保证这条命令将被提交到(be committed to)Raft的log，由于leader可能故障或者输掉选举。即使Raft实例已经被杀掉，该函数也应该优雅地返回。    
由于`Start()`的功能是将接收到的客户端命令追加到自己的本地log，然后给其他所有peers并行发送AppendEntries RPC来迫使其他peer也同意领导者日志的内容，在收到大多数peers的已追加该命令到log的肯定回复后，若该entry的任期等于leader的当前任期，则leader将该entry标记为已提交的(committed)，提升(adavance)`commitIndex`到该entry所在的`index`，并发送`ApplyMsg`消息到`ApplyCh`，相当于应用该entry的命令到状态机。    
可见，`Start()`的结构与`startElection()`的结构类似，都是并行发送RPC，和与发起选举相同的是，`Start()`要根据RPC的回复来统计已追加该entry到本地log的peer的数目，在达到大多数后，提升`commitIndex`，应用该entry的命令到状态机。同时，和发起选举是单次行为不同，`Start()`可能面临客户端的并发请求，所以`Start()`必须进行更精细的处理来应对这种并发的情况。  
### 1.1 并发处理    
回顾[lab 2A中startElection()的结构](../lab2:%20Raft/Part%202A/readme.md#2-并行发送RPC结构)，可以看到在为每个peer封装RPC请求参数时，直接调用`len(rf.log)`来获取日志长度。但这种做法在`Start()`是危险的，考虑这样一种场景：比如客户端连续调用`Star()`三次，提交了三条命令，它们在log中编号分别为`indexN`,`indexN+1`,`indexN+2`。如果首先执行给其他peers发送AppendEntries RPC，并等待并行发送RPC结束的goroutine是`indexN+2`的，而后才执行`indexN`的，那么对于`indexN`的goroutine来说，直接调用`len(rf.log)`得到的日志长度将不是`N`而是错误的`N+2`，这就是并发情况下需要处理的细节问题。    
对于并发，`Start()`要处理的细节的细节有两个：   
1. 主要就是本次提交的命令所在entry的`index`的值。要保证在填充要复制的entries时的结尾索引必须是本次的`index`值，而不是其他并发提交的。同时在确认已将该entry复制到大多数peers后，在将`commitIndex`提升到`index`时，也必须是本次提交的`index`。    
2. 在提升`commitIndex`之前，一定要保证要提升的值`index`大于当前的`commitIndex`，并且该index的任期为当前任期，否则会造成混乱。比如`index=3`的AppendEntries RPC先到达且最终通过了一致性检查，所以提升`commitIndex=3`；随后`index=1`的AppendEntries RPC到达，自然一致性检查一次就直接通过，此时`index(1)<commitIndex(3)`，不应该提升`commitIndex`为1，这就违背了状态机安全属性。   
### 1.2  nextIndex的理解    
正如[Raft学生指南](https://thesquareplanet.com/blog/students-guide-to-raft/)指出的：    
> `nextIndex`是关于领导者和给定跟随者共享的前缀(what prefix)的一种猜测(guess)。它通常相当乐观(optimistic)(我们分享所有内容)，并且仅在负面回复(negative)时才向后移动(moved backwards)。例如，当刚刚选出一个领导者时(when a leader has just been elected)，`nextIndex`被设置为日志末尾的索引的索引(index index at the end of the log)。在某种程度上(in a way)，`nextIndex`用于性能——你只需要将这些内容发送给这个对等点。  
    
`Start()`和`broadcastHeartbeat()`都要发送AppendEntries RPC，而`AppendEntriesArgs`参数的`prevLogIndex`等于leader为该peer保存的`nextIndex`的值减1，但对于两者来说，`nextIndex`值的来源确不一样。  
对于`broadbastHeartbeat()`而言，由于心跳是周期性的常规行为，所以peer:i的`nextIndex`应该取自leader的`rf.nextIndex[i]`。而对于`Start()`来说，由于将新命令追加到了log，所以对于其他所有peers来说，其对应的`nextIndex`都应该更新为当前的log的尾后位置，即`index + 1`。      
### 1.3 达到多数者条件时必须仍处于指定状态  
对于`startElection()`，只有为`Candidate`状态且获得大多数投票，才能变为leader。      
对于`Start()`，只有为`Leader`状态且已将entry复制到了大多数peers，才能提升`commitIndex`。    
因为是为每个peer创建一个goroutine发送RPC并进行RPC回复的处理，根据回复实时统计得到肯定回复的数量。可能出现在给其中一个peer发送RPC时，因为该peer的任期比leader更高，它拒绝了candidate或leder的RPC请求，candidate或leader被拒绝后，切换到`Follower`状态。而与此同时，或者在此之后，该过时的candidaet或leader(已经切换到follwer)，收到了其他peers的大多数的肯定回复，如果这时不对candidate或leader的状态加以判断，那么该过时的candidate或leader因为满足了多数者条件，采取进一步的动作(对于过时的candidate是变为leader，对于过时的leader来说是提升`commitIndex`)，这显然是错误的！所以必须在达到多数者条件时检查下是否仍处于指定状态，如果是，才能进一步执行相关动作。   
## 2. AppendEntries RPC请求处理     
