# Part 3A: Key/Value service without log compaction     
本实验要求你使用lab 2中的Raft库构建一个容错的Key/Value服务。你的Key/Value服务应该是由几个使用Raft来维护复制(matain replication)的key/value服务器组成的一个复制状态机(replicated state machine)。尽管存在一些其他故障(other failures)或网络分区，但只要大多数服务器还活着并可以通信，你的key/value服务就应该继续处理客户端请求。     
但客户端如何与Raft进行交互呢？正如[Raft学生指南的“在Raft之上的应用”](https://thesquareplanet.com/blog/students-guide-to-raft/#applications-on-top-of-raft)一节所述：    
> 你可能对你甚至将(would even)如何(how)根据(in terms of)一个复制的(replicated)日志实现(implement)一个应用程序感到困惑(be confused about)……服务应该被构造为(be constructed as)一个**状态机(state machine)**，其中(where)客户端操作将机器从一个状态转换到(transition)另一个状态。     

你的服务应该支持`Put(key, value)`，`Append(key, arg)`和`Get(key)`这些操作。每个客户端通过`Clerk`的`Put/Append/Get`方法与服务通信。`Clerk`管理与服务器的RPC交互。你的服务应该为调用`Clerk`的`Get/Put/Append`方法的应用程序提供**强一致性**。     
> 你的每个key/value服务器("kvservers")都将有一个关联的(associated)Raft对等点(peer)。Clerks将`Put()`，`Append()`和`Get()`RPCs发送到其关联的Raft是领导者的kvserver。kvserver的代码将`Put/Append/Get`操作提交给Raft，以便Raft日志保存(holds)一个`Put/Append/Get`操作的序列。**所有的(All of)kvserver**都按顺序(in order)从Raft日志中执行操作，将这些操作应用到它们的key/value数据库(databases)；目的是让这些服务器维护key/value数据库的相同(identical)副本(replicas)。     
Clerk有时不知道哪个kvserver是Raft的领导者。如果Clerk将一个RPC发送到错误的kvserver，或者它无法到达kvserver，Clerk应该通过发送到一个不同的kvserver来重试。如果key/value服务器将操作提交到它的Raft日志(并因此将该操作应用到key/value状态机)，则领导者通过响应其RPC将结果报告给Clerk。如果操作未能提交(例如，如果领导者被替换)，服务器报告一个错误，并且Clerk用一个不同的服务器重试。       

- [1. KVServers的内部驱动——执行客户端已提交的命令的应用循环](#1-kvservers的内部驱动执行客户端已提交的命令的应用循环)        
    - [1.1 applyLoop](#11-applyloop)        
    - [1.2 leader中途被替换](#12-leader中途被替换)      
- [2. 客户端请求重复检测](#2-客户端请求重复检测)        
- [3. 客户端超时重试](#3-客户端超时重试)        

## 1. KVServers的内部驱动——执行客户端已提交的命令的应用循环     
### 1.1 applyLoop       
KVServer的数据流程如下图所示：客户端调用`Clerk.Call(Put/Append/Get)`方法发起请求，KVServer的`RPC handler`接受请求，调用`Start()`将Clerk操作作为一个Op(Operation)命令插入到Raft日志中。在调用`Start()`之后，kvserver将需要等待Raft完成一致。Op所在的log entry被提交后，由`applyEntries goroutine`将Op转换为applyMsg写入到`applyCh channel`中。`applyLoop goroutine`负责从`applyCh`中逐个取出`applyMsg`，执行其包含的Op命令，并以`entry`所在的`index`作为索引找到对应的`notifyCh channel`，通过关闭`notifyCh`，通知等待的RPC handler向客户端返回执行结果。正如[Raft学生指南的"在Raft之上的应用"](https://thesquareplanet.com/blog/students-guide-to-raft/#applications-on-top-of-raft)一节所述：      
> 你应该在某个地方(somewhere)有一个循环(loop)，它一次(at the time)取出(takes)一个(one)客户端操作(在所有服务器上以相同的顺序——这是Raft起作用的地方)，并按顺序将每个操作应用到状态机。这个循环应该是你的代码中**唯一**(only)触及(touches)应用程序状态(6.824中的key/value映射)的部分。这意味着你的面向客户端的(client-facing)RPC方法应该只是(simply)将客户端的操作提交给Raft，然后**等待**(wait for)该操作被这个“应用器循环(applier loop)”应用。只有当客户端的命令出现时，它才应该被执行，并读出任何返回值。注意**这包括读请求**！     

![kvserver架构](figures/kvservers%20architecture.png)       
## 1.2 leader中途被替换     
可能存在这样一种情况：leader已经为Clerk的RPC调用了`Start()`，但在请求被提交到日志之前失去了其领导地位。在这种情况下，你应该安排Clerk将请求重新发送到其他服务器，直到它找到新的领导者。正如[Raft学生指南的“在Raft之上的应用”](https://thesquareplanet.com/blog/students-guide-to-raft/#applications-on-top-of-raft)一节描述的：      
> 这提出了另一个问题：你如何知道一个客户端操作何时已经被完成？**在没有失败的情况下**，这很简单——你只要等待你放入日志中的东西回来(即，被传递给`apply()`)。发生这种情况时，你将结果返回给客户端。但是，**如果出现故障会发生什么**？例如，当客户端最初联系你时，你可能已经是领导者，但此后(since)其他人已经被选出，并且你放入日志中的客户端请求已经被丢弃。很明显你需要让客户端再次尝试，但是你如何知道何时告诉它们错误？      
解决这个问题的一个简单方法是记录当你插入客户端操作时，它出现在Raft日志中的位置。一旦该索引上的操作被发送到`apply()`，你就可以根据出现在那个索引处的操作是否实际上是你放在那里的操作，来判断客户端的操作是否成功。如果不是，一次失败已经发生，并且一个错误可以被返回给客户端。       

但是这里存在一个问题：出现这种情况时，至少有两个`RPC handlers`在等待该索引对应的操作。对于过时的leader来说，其RPC handler最终会注意到该索引处出现了不同的操作，从而返回错误。对于新的leader来说，其RPC hander发现该索引处出现了自己插入的操作，将执行结果返回给客户端。所以这里不能通过向`notifyCh channel`写入Op的方式通知等待的`RPC handlers`，因为这样只能有一个等待的RPC handler会读取到Op，另一个等待的RPC handler无法被唤醒。所以我们采取**关闭`notifyCh channel`的方式来通知所有等待的`RPC hanlders`**。     
由于是关闭`notifyCh`，所以无法向`notifyCh`写入`Op`了，那么如何让过时的leader知道index的`Op`不是自己放入的那个呢？通过让服务器注意到`Start()`返回的索引处出现了一个不同的请求，或者注意到Raft的任期已经改变，来检测它已经失去了领导地位。这里从`notifyCh`唤醒后，`RPC handler`需要首先调用`rf.GetState()`获取`isLeader`和`currentTerm`，当其不再是leader或者仍是leader但任期不等于调用`Start()`时的任期，则认为其失去了领导地位，直接返回`Wrongleader`错误。     
现在，我们已经知道了`notifyCh channel`的作用，但其创建与使用过程仍存在微妙之处：`notifyCh`是RPC handler在调用`Start()`之后，根据其返回的`index`在`kv.notifyChs map`中插入的一个通知channel。由于`applyLoop goroutine`和`RPC handler`是两个独立的goroutine，可能存在这样一种情况：`Start()`调用之后，RPC handler还未来得及创建`notifyCh`，其对应的Op已经到达`applyCh channel`，由`applyLoop goroutine`取出并执行，这时`applyLoop`查询`kv.notifyChs map`，查找该index对应的`notifyCh`，将找不到`notifyCh`，从而出现错误！目前，有两种解决方案：       
1. `RPC handler`和`applyLoop goroutine`都创建`notifyCh channel`。两者在创建`notifyCh`前，都先查询下`index`对应的`notifyCh`是否存在，若不存在则创建。这样就可以避免上面的错误。如果`applyLoop goroutine`先于`RPC handler`创建了`notifyCh`，则其关闭`notifyCh`后，查找的`index`的`notifyCh`并等待在其上的`RPC handler`可以收到通知。另外，`RPC handler`先判断`notifyCh`不存在再创建，也保证了出现这里所说的leader被中途替换的情况，假设`applyLoop`没有先于`RPC handler`创建`notifyCh`的话，过时的leader创建了该index的`notifyCh`，而新的leader查询到index的`notifyCh`已经存在，直接使用过时的leader创建的`notifyCh`，从而保证`applyLoop goroutine`在关闭index对应的`notifyCh`时，可以同时通知到新旧leader的`RPC handlers`。       
2. `RPC handler`在调用`Start()`和创建index的`notifyCh`之间一直对`kv.mu`加锁。由于`applyLoop goroutine`在执行`Op`和查找index的`notifyCh`时，也必须持有`kv.mu`锁，而调用`Start()`之前，`applyLoop goroutine`不可能收到该index对应的Op，所以其必须先等待`RPC handler`创建好了`notifyCh`，释放掉`kv.mu`的锁，才能继续执行，也避免了该错误。       

# 2. 客户端请求重复检测     
正如开头提到的，你的服务必须为调用Clerk的`Get/Put/Append`方法的应用程序提供**强一致性**。这里是强一致性的定义：     
> 如果一次(at a time)调用一个，则`Get/Put/Append`方法应该像只有一个状态副本(one copy of its state)的系统那样执行，并且每个调用都应该观察到之前调用的序列隐含的对状态的修改。对于并发调用，返回值和最终状态必须像操作以某种顺序一次执行一个操作那样。如果调用在时间上重叠(overlap)，它就是并发的，比如如果客户端X调用`Clerk.Put()`，然后客户端Y调用`Clerk.Append()`，然后客户端X的调用返回。此外，一次调用必须观察到在本次调用开始前已经完成的所有调用的结果(effect)(所以我们技术上要求线性一致性(linearizability))。        

正如[raft-extended论文的“第8节 客户端交互”](https://pdos.csail.mit.edu/6.824/papers/raft-extended.pdf)所说：        
> Raft的目标是实现线性一致性语义(linearizability semantics)(每个操作似乎是瞬时(instantaneously)完成的，恰好(exactly)一次，在其调用(invocation)和响应(response)之间的某个点(some ponit))。但是，目前描述的Raft可以执行一个命令多次：例如，如果领导者在提交日志条目之后但响应客户端之前崩溃了(crashes)，客户端将向新的领导者重试该命令，造成它被第二次执行。      

因此，为了实现线性一致性，需要某种重复检测(duplicate detection)方案——如果一个客户端向你的服务器发送一个APPEND，没有听到回复(hear back)，并将其重新发送到下一个服务器，则你的apply()函数要确保该APPEND不会被执行两次。       
我们的重复检测方案是：      
1. 每个客户端都需要一个唯一的(unique)客户端ID——可能是一个64位的随机数       
2. 每个客户端为每个请求选择一个序列号seq #，在RPC中发送。在相同的RPC的重新发送中使用相同的seq #。       
3. 客户端在每个RPC中发送客户端ID和seq #，如果它重新发送，则重复seq #        
4. k/v服务负责检测重复客户端请求，为此维护(maintains)由客户端ID索引的表。我们想要保持该重复表小一点，因此每个客户端一个表条目(table entry)，而不是每个RPC一个。每个表条目只包含seq #，和如果已经执行则包含值。       
5. RPC处理程序(RPC handler)首先检查重复表，只有当seq #大于重复表中该客户端的表条目中的seq #时才调用`Start()`。       
6. 每个日志条目(log entry)必须包含客户端ID，seq #。当操作(由日志条目转化为applyMsg再转换为Op)出现在applyCh上时，更新该客户端的table entry中的seq #和值，唤醒等待的RPC处理程序(RPC hanlder)(如果有的话)。        
7. **每个客户端一次(at a time)仅有一个未处理的(outstading)RPC**。       
8. 当服务器收到客户端的#10 RPC时，它可以忘记客户端的较低的条目(lower entries)，**因为(since)这意味着客户端从不会(won't ever)重新发送较旧的(older)RPCs**。       

这里面有一个重要的隐含条件：单个客户端的请求是顺序的，也就是说每个客户端都是在上一个请求返回之后，再执行下一个请求的，同一个客户端的请求都是顺序的，不存在并发。正如[Lab 3：容错的Key/Value服务的“Part 3A：没有日志压缩的Key/Value服务”](https://pdos.csail.mit.edu/6.824/labs/lab-kvraft.html)中的提示所述：       
> 可以假设一个客户端一次(at a time)将只对Clerk做一次调用(make only one call into a clerk)。     

我最开始的重复检测方法没有考虑到这个隐含条件，使用的是`map[id][seqNum]`这样的二维map作为重复表，没有利用同一个客户端的请求是顺序的，没有并发这个特定。这个方案中，对于新请求将其加入到map中并标记为false，当其执行完毕后标记为true。但**这里面存在一个何时将其删除的问题**，正如[Lab 3：容错的Key/Value服务的"Part 3A：没有日志压缩的Key/Value服务"](https://pdos.csail.mit.edu/6.824/labs/lab-kvraft.html)所述：       
> 你的重复检测方案应该快速释放服务器内存，例如通过让每个RPC暗示(imply)客户端已经看到了之前的PRC的回复。     

基于此，在每个RPC请求中会携带客户端上次已经得到回复的请求的序列号`lastAckNum`，服务再执行新的请求时，根据`lastAckNum`及时从二维的去重map中删除已经确认的id.seq对应的信息，以释放服务器内存。        
总结一下，为了去重处理，正如[Raft学生指南的“重复检测”](https://thesquareplanet.com/blog/students-guide-to-raft/#duplicate-detection)一节所说的：        
> 对于每个客户端请求，你需要某种唯一的标识符……有许多方法可以分配(assigning)这样的标识符。一种简单并且相当有效的方法是给每个客户端一个唯一的标识符，然后让它们用一个单调(monotonically)递增的(increasing)序列号(sequence number)标记(tag)每个请求。如果一个客户端重新发送一个请求，则它重用相同的序列号。        

你的服务应该维护一个以client ID作为索引的map，其中的每个条目对应只含有seq number和如果已经执行的请求的结果值。这个去重map应该称为你的状态机的一部分，所有的副本在它们执行时都应该更新自己的去重map，这样如果它们称为leader，信息已经在那里了。      
重复检测需要在`RPC handler`和`applyLoop goroutine`执行两次：        
1. `RPC handler`        
    RPC handler检查去重map，如果`request's seq > map[id].seq`，说明是新的请求，为其调用`Start()`将其插入log并发起一次Raft共识。如果`request's seq <= map[id].seq`，说明是重复请求，直接使用上次执行的结果`map[id].value`回复客户端。        
2. `applyLoop goroutine`        
    applyLoop这里再进行一次重复检测，是为了应对这种情况：leader已经提交并执行了Op但在将结果返回给客户端之前崩溃了。客户端超时并重试，造成Raft log中出现两次该Op的entry。这样，重复检测识别到该Op已经执行过了，直接用上次执行的值返回。对于新的Op，则执行Op并更新去重map中该client ID对应的table entry，用该Op的seq和执行结果更新table entry。       
# 3. 客户端超时重试     
客户端需要重试重试机制，这时因为：我们的服务完全是依靠`applyCh`上出现的`applyMsg`驱动的，如果某peer作为original leader接收了客户端的请求，但在将请求提交之前失去了领导地位，那么该请求就不会出现在applyCh上，接收该请求的RPC handler一直处于等待状态。又因为线性一致性语义假设一个客户端一次只对Clerk进行一次调用，也就是说本次请求没有得到响应前，不会发起新的请求，这就出现了死锁：kvserver等待客户端发起新的请求，其被提交后出现在applyCh上，以便可以唤醒等待的RPC handler；而RPC handler处于等待状态，无法回复客户端请求，本次客户端请求没有得到响应，无法发起新的请求，整个服务陷入死锁。解决方案有两种：      
1. RPC handler本身加入检测leader是否被替换的功能，即我们之前实现的`detectDeposed goroutine`，其周期性地调用`rf.GetState()`，判断是否不再是leader或者任期发生变化，从而检测leader是否被替换。        
2. 客户端请求加入超时重试机制，超时后主动进行重试。     

发生客户端超时重试时，上次的客户端请求被认为是失败的，再次发起重试。        
实现客户端超时重试需要两个步骤：        
1. 将目前Clerk同步的RPC调用通过goroutine改为异步，通过一个`replyCh`可以得到RPC调用的执行结果。      
2. 创建一个类似于`detectDeposed goroutine`的超时检测`requestTimeoutTick goroutine`，周期性检测请求时间是否超时，如果是通过`timeoutCh`通知等待的Clerk发送程序。        
Clerk发送程序同时监听`replyCh`和`timeoutCh`两个事件，如果得到kvserver的请求执行结果则成功返回到客户端；如果执行出错或者超时，则进行重试。     
