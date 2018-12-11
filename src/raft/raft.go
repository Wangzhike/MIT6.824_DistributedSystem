package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	"labrpc"
	"math/rand"
	"sync"
	"time"
)

// import "bytes"
// import "labgob"



//
// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
//
// in Lab 3 you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh; at that point you can add fields to
// ApplyMsg, but set CommandValid to false for these other uses.
//
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int
}

// log entry struct, because AppendEntriesArgs includs []LogEntry,
// so field names must start with capital letters!
type LogEntry struct {
	Command		interface{}		// each entry contains command for state machine,
	Term 		int				// and term when entry was received by leader(fisrt index is 1)
}

// the state of servers
const (
	Follower	int = 0
	Candidate		= 1
	Leader			= 2
)

//
// A Go object implementing a single Raft peer.
//
type Raft struct {
	mu        sync.Mutex          // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]

	// Your data here (2A, 2B, 2C).
	// Look at the paper's Figure 2 for a description of what
	// state a Raft server must maintain.

	applyCh		chan ApplyMsg

	state 		int				// state of server(Follower, Candidate and Leader)
	leaderId	int				// so follower can redirect clients

	applyCond	*sync.Cond		// signal for new committed entry when updating the commitIndex

	leaderCond	*sync.Cond		// signal for heartbeatPeriodTick routine when the peer becomes the leader
	nonLeaderCond 	*sync.Cond	// signal for electionTimeoutTick routine when the peer abdicates the the leader

	electionTimeout	int			// election timout(heartbeat timeout)
	heartbeatPeriod	int			// the period to issue heartbeat RPCs

	latestIssueTime	int64		// 最新的leader发送心跳的时间
	latestHeardTime	int64		// 最新的收到leader的AppendEntries RPC(包括heartbeat)
	// 或给予candidate的RequestVote RPC投票的时间

	electionTimeoutChan	chan bool	// 写入electionTimeoutChan意味着可以发起一次选举
	heartbeatPeriodChan	chan bool	// 写入heartbeatPeriodChan意味leader需要向其他peers发送一次心跳

	// Persistent state on all server
	currentTerm	int				// latest term server has seen(initialized to 0 on fisrt boot,
	// increases monotonically)
	voteFor		int				// candidateId that received vote in current term(or null if none)
	log			[]LogEntry		// log entries

	// Volatile state on all server
	commitIndex	int				// index of highest log entry known to be committed(initialized to 0,
	// increase monotonically)
	lastApplied	int				// index of highest log entry applied to state machine(initialized to 0,
	// increase monotonically)

	// Volatile state on candidate
	nVotes		int				// total num votes that the peer has got

	// Volatile state on leaders
	nextIndex	[]int			// for each server, index of the next log entry to send to that server
	// (initialized to leader last log index + 1)
	matchIndex	[]int			// for each server, index of highest log entry known to be replicated on
	// server(initialized to 0, increases monotonically)

}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {

	var term int
	var isleader bool
	// Your code here (2A).
	rf.mu.Lock()
	term = rf.currentTerm
	if rf.state == Leader {
		isleader = true
	} else {
		isleader = false
	}
	rf.mu.Unlock()

	return term, isleader
}


//
// save Raft's persistent state to stable storage,
// where it can later be retrieved after a crash and restart.
// see paper's Figure 2 for a description of what should be persistent.
//
func (rf *Raft) persist() {
	// Your code here (2C).
	// Example:
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// data := w.Bytes()
	// rf.persister.SaveRaftState(data)
}


//
// restore previously persisted state.
//
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (2C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// field names must start with capital letters!
type AppendEntriesArgs struct {
	Term 			int			// leader's term
	LeaderId		int			// so follower can redirect clients
	PrevLogIndex	int			// index of log entry immediately preceding new ones
	PrevLogTerm		int			// term of PrevLogIndex entry
	Entries			[]LogEntry	// log entries to store(empty for heartbeat; may send
	// more than one for efficiency)
	LeaderCommit	int			// leader's commitIndex
}

type AppendEntriesReply struct {
	Term 			int			// currentTerm, for leader to update itself
	ConflictTerm	int			// the term of conflicting entry
	ConflictFirstIndex	int		// the first index it stores for the term of the conflicting entry
	Success			bool		// true if follower contained entry matching
	// prevLogIndex and prevLogTerm
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// Reply false if term < currentTerm, otherwise continue a "consistency check"
	if rf.currentTerm <= args.Term {

		// If RPC request or response contains term T > currentTerm:
		// set currentTerm = T, convert to follower
		if rf.currentTerm < args.Term {

			DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\targs's term %d is newer\n",
				rf.me, rf.currentTerm, state2name(rf.state), args.Term)

			rf.currentTerm = args.Term

			// 收到来自leader的args.Term大于peer自身的currentTerm的AppendEntries RPC时，表明
			// 目前是存在leader的且自己的任期是过时的，所以需要切换到follower状态，重置选举超时。
			rf.resetElectionTimer()

			// 重置下voteFor，以便可以重新投票
			rf.voteFor = -1

			// if a candidate or leader discovers that its term is out of date, it
			// immediately revert to follower state
			rf.switchTo(Follower)

			// 继续往下，以便一致性检查通过后进行日志复制
		}

		// if the consistency check pass
		if len(rf.log) > args.PrevLogIndex &&
			rf.log[args.PrevLogIndex].Term == args.PrevLogTerm {

			// 收到AppendEntries RPC(包括心跳)，说明存在leader，自己切换为follower状态
			rf.switchTo(Follower)

			// **If** an existing entry conflicts with a new one(same index but
			// different terms), delete the existing entry and all that follow it.
			// 这里的If至关重要。如果follower拥有领导者的日志条目，则follower一定不能(MUST NOT)
			// 截断其日志。leader发送的条目之后的任何内容(any elements of following the entries
			// send by the leader)必须(MUST)保留。

			// 1. 判断follower中log是否已经拥有args.Entries的所有条目，全部有则匹配！
			isMatch := true
			nextIndex := args.PrevLogIndex+1
			end := len(rf.log) - 1
			for i := 0; isMatch && i < len(args.Entries); i++ {
				// 如果args.Entries还有元素，而log已经达到结尾，则不匹配
				if end < nextIndex + i {
					isMatch = false
				} else if rf.log[nextIndex+i].Term != args.Entries[i].Term {
					isMatch = false
				}
			}

			// 2. 如果存在冲突的条目，再进行日志复制
			if isMatch == false {
				// 2.1. 进行日志复制，并更新commitIndex
				rf.log = append(rf.log[:nextIndex], args.Entries...)	// [0, nextIndex) + entries
			}

			DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\tcommitIndex %d while leaderCommit %d" +
				" for leader %d\n", rf.me, rf.currentTerm, state2name(rf.state), rf.commitIndex,
				args.LeaderCommit, args.LeaderId)

			// if leaderCommit > commitIndex, set commitIndex = min(leaderCommit, index of last new entry)
			if args.LeaderCommit > rf.commitIndex {
				rf.commitIndex = args.LeaderCommit
				if rf.commitIndex > len(rf.log) - 1 {
					rf.commitIndex = len(rf.log) - 1
				}
			}

			index := nextIndex + len(args.Entries) - 1
			DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\tconsistency check pass for index %d" +
				" with args's prevLogIndex %d args's prevLogTerm %d\n", rf.me, rf.currentTerm, state2name(rf.state),
				index, args.PrevLogIndex, args.PrevLogTerm)


			// Reset timeout when received leader's AppendEntries RPC
			rf.resetElectionTimer()
			// 接收到leader的心跳，就可以将投票时用于记录授予投票voteFor清零
			rf.voteFor = -1

			// 记录下leaderId
			rf.leaderId = args.LeaderId

			// 更新了commitIndex之后给applyCond条件变量发信号，以应用新提交的entries到状态机
			rf.applyCond.Broadcast()

			reply.Term = rf.currentTerm
			reply.Success = true
			return

		} else {

			nextIndex := args.PrevLogIndex + 1
			index := nextIndex + len(args.Entries) - 1

			DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\tconsistency check failed for index %d" +
				" with args's prevLogIndex %d args's prevLogTerm %d\n",
				rf.me, rf.currentTerm, state2name(rf.state), index, args.PrevLogIndex, args.PrevLogTerm)

			//如果peer的日志长度小于leader的nextIndex
			if len(rf.log) < nextIndex {
				lastIndex := len(rf.log) - 1
				lastTerm := rf.log[lastIndex].Term
				reply.ConflictTerm = lastTerm
				reply.ConflictFirstIndex = lastIndex

				DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\tlog's len %d" +
					" is shorter than args's prevLogIndex %d\n",
					rf.me, rf.currentTerm, state2name(rf.state), len(rf.log), args.PrevLogIndex)
			} else {
				reply.ConflictTerm = rf.log[args.PrevLogIndex].Term
				reply.ConflictFirstIndex = args.PrevLogIndex
				DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\tconsistency check failed" +
					" with args's prevLogIndex %d args's prevLogTerm %d while it's prevLogTerm %d in" +
					" prevLogIndex %d\n", rf.me, rf.currentTerm, state2name(rf.state),
					args.PrevLogIndex, args.PrevLogTerm, rf.log[args.PrevLogIndex].Term, args.PrevLogIndex)
			}
			// 递减reply.ConflictFirstIndex直到index为log中第一个term为reply.ConflictTerm的entry
			for i := reply.ConflictFirstIndex - 1; i >= 0; i-- {
				if rf.log[i].Term != reply.ConflictTerm {
					break
				} else {
					reply.ConflictFirstIndex -= 1
				}
			}
			DPrintf("[AppendEntries]: Id %d Term %d State %s\t||\treply's conflictFirstIndex %d" +
				" and conflictTerm %d\n", rf.me, rf.currentTerm, state2name(rf.state),
				reply.ConflictFirstIndex, reply.ConflictTerm)
		}
	}

	reply.Term = rf.currentTerm
	reply.Success = false
}

func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
	return ok
}


//
// example RequestVote RPC arguments structure.
// field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term 			int			// candidate's term
	CandidateId		int			// candidate requesting vote
	LastLogIndex	int			// index of candidate's last log entry($5.4)
	LastLogTerm		int			// term of candidate's last log entry($5.4)
}

//
// example RequestVote RPC reply structure.
// field names must start with capital letters!
//
type RequestVoteReply struct {
	// Your data here (2A).
	Term 			int			// currentTerm, for candidate to update itself
	VoteGranted		bool		// true means candidate received vote
}

//
// example RequestVote RPC handler.
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).

	rf.mu.Lock()
	defer rf.mu.Unlock()

	// Reply false if term < currentTerm, otherwise continue a "voting process"
	if rf.currentTerm <= args.Term {

		// if one server's current term is smaller than other's, then it updates
		// it current term to the larger value
		if rf.currentTerm < args.Term {

			DPrintf("[RequestVote]: Id %d Term %d State %s\t||\targs's term %d is larger\n",
				rf.me, rf.currentTerm, state2name(rf.state), args.Term)

			rf.currentTerm = args.Term

			// 如果不是follower，则重置voteFor为-1，以便可以重新投票
			rf.voteFor = -1

			// 切换到follower状态
			rf.switchTo(Follower)

			// 继续往下，以便符合条件可以进行投票
		}

		// voteFor is null or candidateId
		if rf.voteFor == -1 || rf.voteFor == args.CandidateId {

			// determine which of two log is more "up-to-date" by comparing
			// the index and term of the last entries in the logs
			lastLogIndex := len(rf.log) - 1
			if lastLogIndex < 0 {
				DPrintf("[RequestVote]: Id %d Term %d State %s\t||\tinvalid lastLogIndex: %d\n",
					rf.me, rf.currentTerm, state2name(rf.state), lastLogIndex)
			}
			lastLogTerm := rf.log[lastLogIndex].Term

			DPrintf("[RequestVote]: Id %d Term %d State %s\t||\tlastLogIndex %d and lastLogTerm %d" +
				" while args's lastLogIndex %d lastLogTerm %d\n", rf.me, rf.currentTerm, state2name(rf.state),
				lastLogIndex, lastLogTerm, args.LastLogIndex, args.LastLogTerm)

			// If the logs have last entries with different terms, then the log with the later term is more up-to-date;
			// otherwise, if the logs end with the same term, then whichever log is longer is more up-to-date.
			// candidate is at least as up-to-date as receiver's log
			if lastLogTerm < args.LastLogTerm ||
				(lastLogTerm == args.LastLogTerm && lastLogIndex <= args.LastLogIndex) {
				rf.voteFor = args.CandidateId
				// reset election timeout
				rf.resetElectionTimer()
				DPrintf("[RequestVote]: Id %d Term %d State %s\t||\tgrant vote for candidate %d\n",
					rf.me, rf.currentTerm, state2name(rf.state), args.CandidateId)
				reply.Term = rf.currentTerm
				reply.VoteGranted = true
				return
			}
		}
	}
	reply.Term = rf.currentTerm
	reply.VoteGranted = false
}

//
// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.
//
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}


//
// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
//
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (2B).
	if term, isLeader = rf.GetState(); isLeader {

		// 1. leader将客户端command作为新的entry追加到自己的本地log
		rf.mu.Lock()
		logEntry := LogEntry{Command:command, Term:rf.currentTerm}
		rf.log = append(rf.log, logEntry)
		index = len(rf.log) - 1
		DPrintf("[Start]: Id %d Term %d State %s\t||\treplicate the command to log index %d\n",
			rf.me, rf.currentTerm, state2name(rf.state), index)
		nReplica := 1
		// 发送AppendEntries RPC时也更新下最近发送时间
		rf.latestIssueTime = time.Now().UnixNano()
		rf.mu.Unlock()

		// 2. 给其他peers并行发送AppendEntries RPC以复制该entry
		go func(nReplica *int, index int, commitIndex int) {
			var wg sync.WaitGroup
			majority := len(rf.peers)/2 + 1
			keep := true
			agreement := false
			isCommitted := false

			rf.mu.Lock()
			DPrintf("[Start]: Id %d Term %d State %s\t||\tcreate an goroutine for index %d" +
				" to issue parallel and wait\n", rf.me, rf.currentTerm, state2name(rf.state), index)
			rf.mu.Unlock()

			for i, _ := range rf.peers {
				// 读keep需要加锁
				rf.mu.Lock()
				if keep == false {
					break
				}
				rf.mu.Unlock()

				if i == rf.me {
					continue
				}
				wg.Add(1)

				// 给peer:i发送AppendEntries RPC
				go func(i int, rf *Raft, nReplica *int, keep *bool) {

					defer wg.Done()
					nextIndex := index + 1

					// 在AppendEntries RPC一致性检查失败后，递减nextIndex，重试
				retry:

					// 因为涉及到retry操作，避免过时的leader的retry操作继续下去
					_, isLeader = rf.GetState()
					if isLeader == false {
						return
					}

					rf.mu.Lock()
					// 封装AppendEntriesArgs参数
					prevLogIndex := nextIndex - 1
					if prevLogIndex < 0 {
						DPrintf("[Start]: Id %d Term %d State %s\t||\tinvalid prevLogIndex %d for index %d" +
							" peer %d\n", rf.me, rf.currentTerm, state2name(rf.state), prevLogIndex, index, i)
					}
					prevLogTerm := rf.log[prevLogIndex].Term
					entries := make([]LogEntry, 0)
					if nextIndex <= index {
						entries = rf.log[nextIndex:index+1]		// [nextIndex, index+1)
					}
					args := AppendEntriesArgs{Term:rf.currentTerm, LeaderId:rf.me,
						PrevLogIndex:prevLogIndex, PrevLogTerm:prevLogTerm,
						Entries:entries, LeaderCommit:commitIndex}
					DPrintf("[Start]: Id %d Term %d State %s\t||\tissue AppendEntries RPC for index %d" +
						" to peer %d with nextIndex %d\n", rf.me, rf.currentTerm, state2name(rf.state), index, i, prevLogIndex+1)
					rf.mu.Unlock()
					var reply AppendEntriesReply

					ok := rf.sendAppendEntries(i, &args, &reply)

					// 发送AppendEntries RPC失败，表明无法和peer建立通信，直接放弃
					if ok == false {
						rf.mu.Lock()
						DPrintf("[Start]: Id %d Term %d State %s\t||\tissue AppendEntries RPC for index %d" +
							" to peer %d failed\n", rf.me, rf.currentTerm, state2name(rf.state), index, i)
						rf.mu.Unlock()
						// Todo: 发送AppendEntries失败，应该直接返回还是重试？直接返回！
						return
					}

					// AppendEntries被拒绝，原因可能是leader任期过时，或者一致性检查未通过
					if reply.Success == false {
						rf.mu.Lock()
						DPrintf("[Start]: Id %d Term %d State %s\t||\tAppendEntries RPC for index %d is rejected" +
							" by peer %d\n", rf.me, rf.currentTerm, state2name(rf.state), index, i)
						// 如果是leader任期过时，需要切换到follower并立即退出。这里应该使用
						// args.Term和reply.Term比较，因为一致性检查就是比较的这两者。而直接
						// 使用rf.currentTerm和reply.Term比较的话，任期过时的可能性就小了。
						// 因为rf.currentTerm在同步发送RPC的过程中可能已经发生改变！
						if args.Term < reply.Term {
							rf.currentTerm = reply.Term
							rf.voteFor = -1
							rf.switchTo(Follower)
							rf.resetElectionTimer()
							*keep = false
							DPrintf("[Start]: Id %d Term %d State %s\t||\tAppendEntries PRC for index %d is rejected by" +
								" peer %d due to newer peer's term %d\n", rf.me, rf.currentTerm, state2name(rf.state),
								index, i, reply.Term)
							//// 任期过时，说明要追加的entry即index索引的entry是过时的，应该从log中删除
							//if index < len(rf.log)-1 {		// 如果index不是最后一个entry的索引
							//	rf.log = append(rf.log[:index], rf.log[index+1:]...)
							//} else {	// 如果index是最后一个entry的索引
							//	rf.log = rf.log[:index]
							//}
							//*nReplica -= 1
							rf.mu.Unlock()
							return

						} else {	// 如果是一致性检查失败，则递减nextIndex，重试

							// 这里递减nextIndex使用了论文中提到的优化策略：
							// If desired, the protocol can be optimized to reduce the number of rejected AppendEntries
							// RPCs. For example,  when rejecting an AppendEntries request, the follower can include the
							// term of the conflicting entry and the first index it stores for that term. With this
							// information, the leader can decrement nextIndx to bypass all of the conflicting entries
							// in that term; one AppendEntries RPC will be required for each term with conflicting entries,
							// rather than one RPC per entry.
							// 只存在reply.ConflictFirstIndex < nextIndex，由于一致性检查是从nextIndex-1(prevLogIndex)处
							// 查看的，所以不会出现reply.ConflictFirstIndex >= nextIndex。
							conflictFirstIndex := reply.ConflictFirstIndex
							conflictTerm := rf.log[conflictFirstIndex].Term
							// 判断conflictFirstIndex处的entry是否和reply的peer的一致，即term相等
							if conflictTerm == reply.ConflictTerm {
								// 相等，则nextIndex直接设置为conflictFirstIndex + 1
								nextIndex = conflictFirstIndex + 1
							} else {
								// 若不等，则递减conflictFirstIndex，直到entry为leader的log中第一个出现conflictTerm的index
								for k := conflictFirstIndex - 1; k >= 0; k-- {
									if rf.log[k].Term != conflictTerm {
										break
									} else {
										conflictFirstIndex -= 1
									}
								}
								nextIndex = conflictFirstIndex + 1
							}
							// 为避免活锁，这里需要判断下prevLogIndex(nextIndex-1)是否等于reply.ConflictFirstIndex，同时prevLogIndex的
							// 任期prevLogTerm是否不等于 reply.ConflictTerm。如果都满足，则说明nextIndex没有前进，遇到“活锁”，这时简单的将
							// nextIndex减1即可。
							if rf.log[nextIndex-1].Term != reply.ConflictTerm &&
								nextIndex - 1 == reply.ConflictFirstIndex {
								nextIndex -= 1
							}
							DPrintf("[Start]: Id %d Term %d State %s\t||\tAppendEntries RPC for index %d is rejected by" +
								" peer %d due to the consistency check failed\n", rf.me, rf.currentTerm,
								state2name(rf.state), index, i)
							DPrintf("[Start]; Id %d Term %d State %s\t||\tretry AppendEntries RPC with" +
								" conflictFirstIndex %d and conflictTerm %d nextIndex %d\n", rf.me, rf.currentTerm,
								state2name(rf.state), conflictFirstIndex, conflictTerm, nextIndex)
							rf.mu.Unlock()
							goto retry

						}
					} else {	// AppendEntries RPC发送成功

						rf.mu.Lock()
						DPrintf("[Start]: Id %d Term %d State %s\t||\tsend AppendEntries PRC for index %d to peer %d success\n",
							rf.me, rf.currentTerm, state2name(rf.state), index, i)
						// 如果当前index更大，则更新该peer对应的nextIndex和matchIndex
						if rf.matchIndex[i] < index {
							rf.nextIndex[i] = index + 1
							rf.matchIndex[i] = index
						}
						*nReplica += 1
						DPrintf("[Start]: Id %d Term %d State %s\t||\tnReplica %d for index %d\n",
							rf.me, rf.currentTerm, state2name(rf.state), *nReplica, index)

						// 如果已经将该entry复制到了大多数peers，接着检查index编号的这条entry的任期
						// 是否为当前任期，如果是则可以提交该条目
						if agreement == false && rf.state == Leader && *nReplica >= majority {
							agreement = true
							DPrintf("[Start]: Id %d Term %d State %s\t||\thas replicated the entry with index %d" +
								" to the majority with nReplica %d\n", rf.me, rf.currentTerm, state2name(rf.state),
								index, *nReplica)
							// 如果index大于commitIndex，而且index编号的entry的任期等于当前任期，提交该entry
							if rf.commitIndex < index && rf.log[index].Term == rf.currentTerm {
								DPrintf("[Start]: Id %d Term %d State %s\t||\tadvance the commitIndex to %d\n",
									rf.me, rf.currentTerm, state2name(rf.state), index)
								isCommitted = true

								rf.commitIndex = index
								// 更新了commitIndex可以给applyCond条件变量发信号，
								// 以应用新提交的entries到状态机
								DPrintf("[Start]: Id %d Term %d State %s\t||\tapply updated commitIndex %d to applyCh\n",
									rf.me, rf.currentTerm, state2name(rf.state), rf.commitIndex)
								rf.applyCond.Broadcast()
							}
						}
						// 当被提交的entries被复制到所有peers后，可以发送一次心跳通知其他peers更新commitIndex
						if *nReplica == len(rf.peers) && isCommitted {
							// 同时发送给其他peers发送一次心跳，使它们更新commitIndex
							go rf.broadcastHeartbeat()
						}

						rf.mu.Unlock()
					}

				}(i, rf, nReplica, &keep)
			}

			// 等待所有发送AppendEntries RPC的goroutine退出
			wg.Wait()

		}(&nReplica, index, rf.commitIndex)

	}
	return index, term, isLeader
}

// 按顺序(in order)发送已提交的(committed)日志条目到applyCh的goroutine。
// 该goroutine是单独的(separate)、长期运行的(long-running)，在没有新提交
// 的entries时会等待条件变量；当更新了commitIndex之后会给条件变量发信号，
// 以唤醒该goroutine执行提交。
func (rf *Raft) applyEntries() {
	for {

		rf.mu.Lock()
		commitIndex := rf.commitIndex
		lastApplied := rf.lastApplied
		DPrintf("[applyEntries]: Id %d Term %d State %s\t||\tlastApplied %d and commitIndex %d\n",
			rf.me, rf.currentTerm, state2name(rf.state), lastApplied, commitIndex)
		rf.mu.Unlock()

		if lastApplied == commitIndex {
			rf.mu.Lock()
			rf.applyCond.Wait()
			rf.mu.Unlock()
		} else {
			for i := lastApplied+1; i <= commitIndex; i++ {

				rf.mu.Lock()
				applyMsg := ApplyMsg{CommandValid:true, Command:rf.log[i].Command,
					CommandIndex:i}
				rf.lastApplied = i
				DPrintf("[applyEntries]: Id %d Term %d State %s\t||\tapply command of index %d to applyCh\n",
					rf.me, rf.currentTerm, state2name(rf.state), i)
				rf.mu.Unlock()
				rf.applyCh <- applyMsg

			}
		}
	}
}

//
// the tester calls Kill() when a Raft instance won't
// be needed again. you are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance.
//
func (rf *Raft) Kill() {
	// Your code here, if desired.
}


func state2name(state int) string {
	var name string
	if state == Follower {
		name = "Follower"
	} else if state == Candidate {
		name = "Candidate"
	} else if state == Leader {
		name = "Leader"
	}
	return name
}

// 统一处理Raft状态转换。这么做的目的是为了没有遗漏的处理nonLeader与leader状态之间转换时需要给对应
// 的条件变量发信号的工作。：
// 	Leader -> nonLeader(Follower): rf.nonLeaderCond.broadcast()
//	nonLeader(Candidate) -> Leader: rf.leaderCond.broadcast()
// 为了避免死锁，该操作不加锁，由外部加锁保护！
func (rf *Raft) switchTo(newState int) {
	oldState := rf.state
	rf.state = newState
	if oldState == Leader && newState == Follower {
		rf.nonLeaderCond.Broadcast()
	} else if oldState == Candidate && newState == Leader {
		rf.leaderCond.Broadcast()
	}
}

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

// 心跳发送周期检查器。leader检查距离上次发送心跳的时间(latestIssueTime)是否超过了心跳周期(heartbeatPeriod)，
// 若超过则写入数据到heartbeatPeriodChan，以通知发送心跳
func (rf *Raft) heartbeatPeriodTick() {
	for {
		// 如果peer不是leader，则等待leaderCond条件变量
		if term, isLeader := rf.GetState(); isLeader == false {
			rf.mu.Lock()
			rf.leaderCond.Wait()
			rf.mu.Unlock()
		} else {
			rf.mu.Lock()
			elapseTime := time.Now().UnixNano() - rf.latestIssueTime
			if int(elapseTime/int64(time.Millisecond)) >= rf.heartbeatPeriod {
				DPrintf("[HeartbeatPeriodTick]: Id %d Term %d State %s\t||\theartbeat period elapsed," +
					" issue heartbeat\n", rf.me, term, state2name(rf.state))
				rf.heartbeatPeriodChan <- true
			}
			rf.mu.Unlock()
			// 休眠10ms，作为tick的时间间隔。如果休眠时间太短，比如1ms，将导致频繁检查选举超时，
			// 造成测量到的user时间，即CPU时间增长，可能超过5秒。
			time.Sleep(time.Millisecond*10)
		}
	}
}

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

// leader给其他peers广播一次心跳。因为发送心跳也要进行一致性检查，
// 为了不因为初始时的日志不一致而使得心跳发送失败，而其他peers因为
// 接收不到心跳而心跳超时，进而发起不需要的(no-needed)选举，所以
// 发送心跳也需要在一致性检查失败时进行重试。
func (rf *Raft) broadcastHeartbeat() {

	// 非leader不能发送心跳
	if _, isLeader := rf.GetState(); isLeader == false {
		return
	}

	rf.mu.Lock()
	// 发送心跳时更新下发送时间
	rf.latestIssueTime = time.Now().UnixNano()
	rf.mu.Unlock()

	go func() {
		var wg sync.WaitGroup
		keep := true

		for i, _ := range rf.peers {

			// 读keep需要加锁
			rf.mu.Lock()
			if keep == false {
				break
			}
			rf.mu.Unlock()

			if i == rf.me {
				continue
			}
			wg.Add(1)

			go func(i int, rf *Raft, keep *bool) {
				defer wg.Done()

				// 在AppendEntries RPC一致性检查失败后，递减nextIndex，重试
			retry:

				// 因为涉及到retry操作，避免过时的leader的retry操作继续下去
				if _, isLeader := rf.GetState(); isLeader == false {
					return
				}

				rf.mu.Lock()
				// 封装AppendEntriesArgs参数
				prevLogIndex := rf.nextIndex[i] - 1
				if prevLogIndex < 0 {
					DPrintf("[Broadcast]: Id %d Term %d State %s\t||\tinvalid prevLogIndex %d for peer %d\n",
						rf.me, rf.currentTerm, state2name(rf.state), prevLogIndex, i)
				}
				prevLogTerm := rf.log[prevLogIndex].Term
				// Todo:概念上将心跳不携带entries，这指的是当nextIndex为log的尾后位置时的一般情况。
				// 但是如果nextIndex小于log的尾后位置，这是心跳必须携带entries，因为这次心跳可能就会
				// 通过一致性检查，并可能提升commitIndex，这时会给applyCond条件变量发信号以提交
				// [lastApplied+1, commitIndex]之间的entries。如果此次心跳没有携带entries，则不会有
				// 日志追加，所以提交的可能是和leader不一致的过时的entries，这就出现了严重错误。所以
				// 这种情况下心跳要携带entries。
				entries := rf.log[prevLogIndex+1:]
				args := AppendEntriesArgs{Term:rf.currentTerm, LeaderId:rf.me,
					PrevLogIndex:prevLogIndex, PrevLogTerm:prevLogTerm,
					Entries:entries, LeaderCommit:rf.commitIndex}
				DPrintf("[Broadcast]: Id %d Term %d State %s\t||\tissue heartbeat to peer %d" +
					" with nextIndex %d\n", rf.me, rf.currentTerm, state2name(rf.state), i, prevLogIndex+1)
				rf.mu.Unlock()
				var reply AppendEntriesReply

				ok := rf.sendAppendEntries(i, &args, &reply)

				// 心跳发送失败，表明无法和peer建立通信，直接退出
				if ok == false {
					rf.mu.Lock()
					DPrintf("[Broadcast]: Id %d Term %d State %s\t||\tissue heartbeat to peer %d failed\n",
						rf.me, rf.currentTerm, state2name(rf.state), i)
					rf.mu.Unlock()
					return
				}

				// heartbeat被拒绝，原因可能是leader任期过时，或者一致性检查没有通过。
				// 发送心跳也可能出现一致性检查不通过，因为一致性检查是查看leader的nextIndex之前的
				// entry和指定peer的log中那个索引的日志是否匹配。即使心跳中不携带任何日志，但一致性
				// 检查仍会因为nextIndex而失败，这时需要递减nextIndex然后重试。
				if reply.Success == false {

					rf.mu.Lock()
					DPrintf("[Broadcast]: Id %d Term %d State %s\t||\theartbeat is rejected by peer %d\n",
						rf.me, rf.currentTerm, state2name(rf.state), i)

					// leader任期过时，需要切换到follower
					if rf.currentTerm < reply.Term {
						// If RPC request or response contains term T > currentTerm, set currentTerm = T,
						// convert to follower
						rf.currentTerm = reply.Term
						rf.voteFor = -1
						rf.switchTo(Follower)
						rf.resetElectionTimer()
						*keep = false
						DPrintf("[Broadcast]: Id %d Term %d State %s\t||\theartbeat is rejected by peer %d" +
							" due to newer peer's term %d\n", rf.me, rf.currentTerm, state2name(rf.state), i, reply.Term)
						rf.mu.Unlock()
						return
					} else {	// 如果是一致性检查未通过，则递减nextIndex，重试

						conflictFirstIndex := reply.ConflictFirstIndex
						conflictTerm := rf.log[conflictFirstIndex].Term
						// 判断conflictFirstIndex处的entry是否和reply的peer一致，即term相等
						if conflictTerm == reply.ConflictTerm {
							// 相等，则nextIndex直接设置为conflictFirstIndex + 1
							rf.nextIndex[i] = conflictFirstIndex + 1
						} else {
							// 若不等，则递减conflictFirstIndex，直到entry为leader的log中第一个出现conflictTerm的index
							for k := conflictFirstIndex - 1; k >= 0; k-- {
								if rf.log[k].Term != conflictTerm {
									break
								} else {
									conflictFirstIndex -= 1
								}
							}
							rf.nextIndex[i] = conflictFirstIndex + 1
						}
						// 为避免活锁，这里需要判断下prevLogIndex(rf.nextIndex[i]-1)的任期是否等于reply.ConflictTerm。
						// 如果不等，则说明rf.nextIndex[i]没有前进，遇到“活锁”，这时简单的将其减1即可。
						nextIndex := rf.nextIndex[i]
						if nextIndex - 1 == reply.ConflictFirstIndex &&
							rf.log[nextIndex-1].Term != reply.ConflictTerm {
							rf.nextIndex[i] -= 1
						}

						DPrintf("[Broadcast]: Id %d Term %d State %s\t||\theartbeat is rejected by peer %d" +
							" due to the consistency check failed\n", rf.me, rf.currentTerm, state2name(rf.state), i)
						DPrintf("[Broadcast]: Id %d Term %d State %s\t||\tretry heartbeat with" +
							" conflictFirstIndex %d and conflictTerm %d nextIndex %d\n", rf.me, rf.currentTerm,
							state2name(rf.state), conflictFirstIndex, conflictTerm, rf.nextIndex[i])
						rf.mu.Unlock()
						goto retry
					}

				} else {
					// 心跳发送成功
					rf.mu.Lock()
					// 更新下该peer对应的nextIndex和matchIndex
					if rf.nextIndex[i] < len(rf.log) {
						rf.nextIndex[i] = len(rf.log)
						rf.matchIndex[i] = rf.nextIndex[i] - 1
					}
					//rf.matchIndex[i] = rf.nextIndex[i] - 1
					DPrintf("[Broadcast]: Id %d Term %d State %s\t||\tsend heartbeat to peer %d success\n",
						rf.me, rf.currentTerm, state2name(rf.state), i)
					rf.mu.Unlock()
				}

			}(i, rf, &keep)

		}

		//等待所有发送goroutine结束
		wg.Wait()

	}()
}


// 重置election timer，不加锁
func (rf *Raft) resetElectionTimer() {
	// 随机化种子以产生不同的伪随机数序列
	rand.Seed(time.Now().UnixNano())
	// 重新选举随机的electionTimeout
	rf.electionTimeout = rf.electionTimeout + rand.Intn(300-150)
	// 因为重置了选举超时，所以也需要更新latestHeardTime
	rf.latestHeardTime = time.Now().UnixNano()
}

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

//
// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
//
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (2A, 2B, 2C).

	// 调用Make()时是创建该Raft实例，此时该实例没有并发的goroutines，无需加锁
	// Part 2A
	rf.applyCh = applyCh
	rf.state = Follower
	rf.leaderId = -1
	rf.applyCond = sync.NewCond(&rf.mu)
	rf.leaderCond = sync.NewCond(&rf.mu)
	rf.nonLeaderCond = sync.NewCond(&rf.mu)
	rf.heartbeatPeriod = 200	// 因为要求leader每秒发送的心跳RPCs不能超过10次，
	// 这里心跳周期取最大值100ms
	rf.resetElectionTimer()
	rf.electionTimeoutChan = make(chan bool)
	rf.heartbeatPeriodChan = make(chan bool)

	// initialized to 0 on first boot, increases monotonically
	rf.currentTerm = 0
	rf.voteFor = -1		// -1意味着没有给任何peer投票

	rf.commitIndex = 0
	rf.lastApplied = 0

	// each entry of log contains command for state machine, and term
	// when entry was received by leader(**fisrt index is 1**)
	// 也就是说，log中第0个元素不算有效entry，合法entry从下标1计算。
	rf.log = make([]LogEntry, 0)
	rf.log = append(rf.log, LogEntry{Term:0})

	// 初始化nextIndex[]和matchIndex[]的大小
	size := len(rf.peers)
	rf.nextIndex = make([]int, size)
	// matchIndex元素的默认初始值即为0
	rf.matchIndex = make([]int, size)

	go rf.electionTimeoutTick()
	go rf.heartbeatPeriodTick()
	go rf.eventLoop()
	go rf.applyEntries()

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())


	return rf
}