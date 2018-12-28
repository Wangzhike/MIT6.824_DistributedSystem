# Part 2C: Persistence      
> 如果一个基于Raft(Raft-based)的服务器重新启动，它应该从停止的地方(when it left off)恢复(resume)服务。这要求Raft保持在重启后仍然存在的(that survives a reboot)持久状态(persistent state)。论文图2提到了哪些状态(which state)应该是持久的，并且raft.go包含了如何保存(save)和恢复(restore)持久状态的示例。    
通过添加保存和恢复持久状态的代码，来完成raft.go中`persist()`和`readPersist()`函数。     
现在你需要确定(determine)在Raft协议中的哪些点(what points in the Raft protocol)你的服务器被要求持久化其状态，并在这些地方插入对`persist()`的调用。在`Raft.Make()`中已经有对`readPersist()`的调用。      

## 1. 加速的日志回溯优化(The accelerated log backtracking optimization)     
正如[Raft学生指南](https://thesquareplanet.com/blog/students-guide-to-raft/)中的顺便提一下优化(An aside on optimizations)所述：     
> Raft论文包含了几个(a couple of)有趣的可选特性(optional features)。在6.824，我们要求学生实现其中两个：日志压缩(log compaction)(第7节)和加速的(accelerated)日志回溯(backtracking)(第8页的左上角)。前者(The former)对于避免日志无限制增长(growing without bound)是必要的(necessary)，而后者(the latter)对于使(bringing)陈旧的(stale)跟随者快速更新(up to date quickly)是有用的。     
加速的日志回溯优化是非常不明确的(underspecified)，可能因为作者认为它不是大多数部署所必须的(do not see it as being necessary for most deployments)。从文本中不清楚领导者应该如何利用从客户端发回的(sent back from client)冲突的(conflicting)索引(index)和任期(term)来确定(determine)要使用的`nextIndex`(to determine what nextIndex to use)。我们认为(believe)作者**可能(probobly)**希望你遵循的协议(protocol)是：   
如果跟随者的日志中没有`prevLogIndex`，它应该以`conflictIndex = len(log)`以及`conflictTerm = None`返回。     
如果跟随者的日志中确实有(does have)`prevLogIndex`，但该任期不匹配，它应该返回`conflictTerm = log[prevLogIndex].Term`，然后在其日志中搜索其任期等于`conflictTerm`的条目的第一个索引。    
收到冲突的响应后(Upon receiving a conflict response)，领导者应该首先(first)在其日志中搜索`conflictTerm`。如果它在日志中找到了一个具有该任期的条目，它应该将`nextIndex`设置为超出(beyond)日志中该任期的**最后一个(last)**条目的索引。    
如果它没有找到具有那个任期的条目，它应该设置`nextIndex = conflictIndex`。   
一个折中的(half-way)解决方案是只使用`conflictIndex`(而忽略`conflictTerm`)，这简化了(simplifies)实现，但是有时候领导者会向跟随者发送更多的日志条目，而不是使它们快速更新确切(strictly)需要的条目。       

[Part 2B: Log Replication的2.2 减少被拒绝的AppendEntries RPC的次数](../Part%202B/readme.md#22-减少被拒绝的appendentries-rpc的次数)，我们自己提出了一种优化策略，相对于上面给出的策略而言，我们的方案比上面给出的只使用`conflictIndex`的折中的方案，还会使领导者向跟随者发送更多的日志条目，因为我们的方案在收到冲突的回复时，会检查`reply.conflictFirstIndex`处entry的任期是否等于`reply.conflictTerm`，如果不等，leader也会采用类似的优化手段，递减`conflictFirstIndex`，直到其为该任期的第一个索引，设置`nextIndex = conflictIndex + 1`；而上面的折中的方法此时会设置`nextIndex = conflictIndex`，这样做得到的`nextIndex`肯定比我们方案得到的更靠后，而且也不会存在“活锁”问题。   
但是，我们给出的优化方案中关于`reply.conflictIndex`(也就是我们方案中的`reply.conflictFirstIndex`)的大小分析是完全正确的：   
> **`conflictFirstIndex`一定小于`nextIndex`**。因为一致性检查是从`prevLogIndex(nextIndex-1)`处查看的，所以`conflictTerm`**至多是**`prevLogIndex`对应entry的任期，而`conflictFirstIndex`作为`conflictTerm`的第一个出现的索引，**至多等于**`prevLogIndex`，所以必然小于`nextIndex`。      

接下来分析如何实现上面给出的[Raft学生指南](https://thesquareplanet.com/blog/students-guide-to-raft/)给出的优化策略：    
首先，如果跟随者的日志中没有`prevLogIndex`，也就是说`reply.conflictTerm = None`，则领导者肯定无法从其日志中搜索到该任期，所以直接设置`nextIndex = conflictIndex`。否则，分为两步：  
1. leader搜索其log检查是否具有`conflictTerm`的entry。   
2. 如果有，则继续搜索其log，以找到任期为`conflictTerm`的最后一个entry。     

其中，在第1步搜索是否具有`conflictTerm`的entry时，因为`conflictFirstIndex`小于`nextIndex`，所以可以直接从`conflictFirstIndex`处开始往前，在`[0, conflictFirstIndex]`的范围内查找是否具有`conflictTerm`的entry。这是一般做法。也可以根据`reply.conflictTerm`和`log[reply.conflictFirstIndex].Term`的大小关系，做进一步的细化分析：   
![加速的日志回溯优化](figures/加速的日志回溯优化.png)       
如图所示，灰色部分表示未知任期的entries。   
1. `reply.conflictTerm = log[reply.conflictFirstIndex].Term`   
如(a)所示，这时步骤1可以确定存在具有`conflictTerm`的条目，步骤2从`reply.conflictFirstIndex`处向前搜索，直到抵达`nextIndex`之前。     
2. `reply.conflictTerm < log[reply.conflictFristIndex].Term`    
如(b)所示，这时步骤1可能存在具有`conflictTerm`的条目，如果存在，则发现点即步骤2的搜索结果，即任期为`conflictTerm`的最后一个entry。      
3. `reply.conflictTerm > log[reply.conflictFirstIndex].Term`    
如(c)所示，这时步骤1无法找到具有`conflictTerm`的条目。因为搜索是从`reply.conflictFirstIndex`处往前的，并且日志条目的任期是非降序的，所以leader的log中从`[0, conflictFirstIndex]`之间不存在任期等于`conflictTerm`的entry。       

代码实现如下：  
```go
func (rf *Raft) getNextIndex(reply AppendEntriesReply, nextIndex int) int {
    // 这里递减nextIndex使用了论文中提到的优化策略：
    // If desired, the protocol can be optimized to reduce the number of rejected AppendEntries
    // RPCs. For example,  when rejecting an AppendEntries request, the follower can include the
    // term of the conflicting entry and the first index it stores for that term. With this
    // information, the leader can decrement nextIndx to bypass all of the conflicting entries
    // in that term; one AppendEntries RPC will be required for each term with conflicting entries,
    // rather than one RPC per entry.
    // 只存在reply.ConflictFirstIndex < nextIndex，由于一致性检查是从nextIndex-1(prevLogIndex)处
    // 查看的，所以不会出现reply.ConflictFirstIndex >= nextIndex。

    // reply's conflictTerm=0，表示None。说明peer:i的log长度小于nextIndex。
    if reply.ConflictTerm == 0 {
        // If it does not find an entry with that term, it should set nextIndex = conflictIndex
        nextIndex = reply.ConflictFirstIndex
    } else {	// peer:i的prevLogIndex处的任期与leader不等
        // leader搜索它的log确认是否存在等于该任期的entry
        conflictIndex := reply.ConflictFirstIndex
        conflictTerm := rf.Log[conflictIndex].Term
        // 只有conflictTerm大于或等于reply's conflictTerm，才有可能或一定找得到任期相等的entry
        if conflictTerm >= reply.ConflictTerm {
            // 从reply.ConflictFirstIndex处开始向搜索，寻找任期相等的entry
            for i := conflictIndex; i > 0; i-- {
                if rf.Log[i].Term == reply.ConflictTerm {
                    break
                }
                conflictIndex -= 1
            }
            // conflictIndex不为0，leader的log中存在同任期的entry
            if conflictIndex != 0 {
                // 向后搜索，使得conflictIndex为最后一个任期等于reply.ConflictTerm的entry
                for i := conflictIndex+1; i < nextIndex; i++ {
                    if rf.Log[i].Term != reply.ConflictTerm {
                        break
                    }
                    conflictIndex += 1
                }
                nextIndex = conflictIndex + 1
            } else {	// conflictIndex等于0，说明不存在同任期的entry
                nextIndex = reply.ConflictFirstIndex
            }
        } else {	// conflictTerm < reply.ConflictTerm，并且必须往前搜索，所以一定找不到任期相等的entry
            nextIndex = reply.ConflictFirstIndex
        }

    }
    return nextIndex
}
```

