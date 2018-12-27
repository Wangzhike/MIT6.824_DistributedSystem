# Part 2C: Persistence      
> 如果一个基于Raft(Raft-based)的服务器重新启动，它应该从停止的地方(when it left off)恢复(resume)服务。这要求Raft保持在重启后仍然存在的(that survives a reboot)持久状态(persistent state)。论文图2提到了哪些状态(which state)应该是持久的，并且raft.go包含了如何保存(save)和恢复(restore)持久状态的示例。    
通过添加保存和恢复持久状态的代码，来完成raft.go中`persist()`和`readPersist()`函数。     
现在你需要确定(determine)在Raft协议中的哪些点(what points in the Raft protocol)你的服务器被要求持久化其状态，并在这些地方插入对`persist()`的调用。在`Raft.Make()`中已经有对`readPersist()`的调用。      

## 1. 加速的日志回溯优化(The accelerated log backtracking optimization)     
正如[Raft学生指南](https://thesquareplanet.com/blog/students-guide-to-raft/)中的顺便提一下优化(An aside on optimizations)所述：     
> Raft论文包含了几个(a couple of)有趣的可选特性(optional features)。在6.824，我们要求学生实现其中两个：日志压缩(log compaction)(第7节)和加速的(accelerated)日志回溯(backtracking)(第8页的左上角)。前者(The former)对于避免日志无限制增长(growing without bound)是必要的(necessary)，而后者(the latter)对于使(bringing)陈旧的(stale)跟随者快速更新(up to date quickly)是有用的。     
加速的日志回溯优化是非常不明确的(underspecified)，可能因为作者认为它不是大多数部署所必须的(do not see it as being necessary for most deployments)。从文本中不清楚领导者应该如何利用从客户端发回的(sent back from client)冲突的(conflicting)索引(index)和任期(term)来确定(determine)要使用的`nextIndex`(to determine what nextIndex to use)。我们认为(believe)作者**可能(probobly)**希望你遵循的协议(protocol)是：   
