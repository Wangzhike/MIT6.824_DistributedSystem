# Part 3A: Key/Value service without log compaction     
本实验要求你使用lab 2中的Raft库构建一个容错的Key/Value服务。你的Key/Value服务应该是由几个使用Raft来维护复制(matain replication)的key/value服务器组成的一个复制状态机(replicated state machine)。尽管存在一些其他故障(other failures)或网络分区，但只要大多数服务器还活着并可以通信，你的key/value服务就应该继续处理客户端请求。     
但客户端如何与Raft进行交互呢？正如[Raft学生指南的在Raft之上的应用](https://thesquareplanet.com/blog/students-guide-to-raft/#applications-on-top-of-raft)一节所述：    
> 你可能对你甚至将(would even)如何(how)根据(in terms of)一个复制的(replicated)日志实现(implement)一个应用程序感到困惑(be confused about)……服务应该被构造为(be constructed as)一个**状态机(state machine)**，其中(where)客户端操作将机器从一个状态转换到(transition)另一个状态。     

你的服务应该支持`Put(key, value)`，`Append(key, arg)`和`Get(key)`这些操作。每个客户端通过`Clerk`的`Put/Append/Get`方法与服务通信。`Clerk`管理与服务器的RPC交互。你的服务应该为调用`Clerk`的`Get/Put/Append`方法的应用程序提供**强一致性**。     
> 你的每个key/value服务器("kvservers")都将有一个关联的(associated)Raft对等点(peer)。Clerks将`Put()`，`Append()`和`Get()`RPCs发送到其关联的Raft是领导者的kvserver。kvserver的代码将`Put/Append/Get`操作提交给Raft，以便Raft日志保存(holds)一个`Put/Append/Get`操作的序列。**所有的(All of)kvserver**都按顺序(in order)从Raft日志中执行操作，将这些操作应用到它们的key/value数据库(databases)；目的是让这些服务器维护key/value数据库的相同(identical)副本(replicas)。     
Clerk有时不知道哪个kvserver是Raft的领导者。如果Clerk将一个RPC发送到错误的kvserver，或者它无法到达kvserver，Clerk应该通过发送到一个不同的kvserver来重试。如果key/value服务器将操作提交到它的Raft日志(并因此将该操作应用到key/value状态机)，则领导者通过响应其RPC将结果报告给Clerk。如果操作未能提交(例如，如果领导者被替换)，服务器报告一个错误，并且Clerk用一个不同的服务器重试。       

## 1. KVServers的内部驱动——执行客户端已提交的命令的应用循环     
KVServer的数据流程如下图所示：客户端调用`Clerk.Call(Put/Append/Get)`方法发起请求，KVServer的`RPC handler`接受请求，并将Clerk操作作为一个Op(Operation)命令插入到Raft日志中。Op所在的log entry被提交后，由`applyEntries goroutine`将Op转换为applyMsg写入到`applyCh channel`中。`applyLoop goroutine`负责从`applyCh`中逐个取出`applyMsg`，执行其包含的Op命令，并以`entry`所在的`index`作为索引找到对应的`notifyCh channel`，通过关闭`notifyCh`，通知等待的RPC handler向客户端返回执行结果。正如[Raft学生指南的"在Raft之上的应用"](https://thesquareplanet.com/blog/students-guide-to-raft/#applications-on-top-of-raft)一节所述：      
> 你应该在某个地方(somewhere)有一个循环(loop)，它一次(at the time)取出(takes)一个(one)客户端操作(在所有服务器上以相同的顺序——这是Raft起作用的地方)，并按顺序将每个操作应用到状态机。这个循环应该是你的代码中**唯一**(only)触及(touches)应用程序状态(6.824中的key/value映射)的部分。这意味着你的面向客户端的(client-facing)RPC方法应该只是(simply)将客户端的操作提交给Raft，然后**等待**(wait for)该操作被这个“应用器循环(applier loop)”应用。只有当客户端的命令出现时，它才应该被执行，并读出任何返回值。注意**这包括读请求**！     

![kvserver架构](figures/kvservers%20architecture.png)       
