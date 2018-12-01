package mapreduce

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"
)

type KeyValueList []KeyValue

func (kvs KeyValueList) Len() int {
	return len(kvs)
}

func (kvs KeyValueList) Less(i, j int) bool {
	return strings.Compare(kvs[i].Key, kvs[j].Key) == -1
}

func (kvs KeyValueList) Swap(i, j int) {
	kvs[i], kvs[j] = kvs[j], kvs[i]
}

func doReduce(
	jobName string, // the name of the whole MapReduce job
	reduceTask int, // which reduce task this is
	outFile string, // write the output here
	nMap int, // the number of map tasks that were run ("M" in the paper)
	reduceF func(key string, values []string) string,
) {
	//
	// doReduce manages one reduce task: it should read the intermediate
	// files for the task, sort the intermediate key/value pairs by key,
	// call the user-defined reduce function (reduceF) for each key, and
	// write reduceF's output to disk.
	//
	// You'll need to read one intermediate file from each map task;
	// reduceName(jobName, m, reduceTask) yields the file
	// name from map task m.
	//
	// Your doMap() encoded the key/value pairs in the intermediate
	// files, so you will need to decode them. If you used JSON, you can
	// read and decode by creating a decoder and repeatedly calling
	// .Decode(&kv) on it until it returns an error.
	//
	// You may find the first example in the golang sort package
	// documentation useful.
	//
	// reduceF() is the application's reduce function. You should
	// call it once per distinct key, with a slice of all the values
	// for that key. reduceF() returns the reduced value for that key.
	//
	// You should write the reduce output as JSON encoded KeyValue
	// objects to the file named outFile. We require you to use JSON
	// because that is what the merger than combines the output
	// from all the reduce tasks expects. There is nothing special about
	// JSON -- it is just the marshalling format we chose to use. Your
	// output code will look something like this:
	//
	// enc := json.NewEncoder(file)
	// for key := ... {
	// 	enc.Encode(KeyValue{key, reduceF(...)})
	// }
	// file.Close()
	//
	// Your code here (Part I).
	//

	// 逐个读取M个中间文件，对每个文件逐个解析出KeyValue对象，直到遇到EOF出错为止，
	// 将每个文件的反序列化结果保存到一个临时切片中。对其排序，并将切片合并后排序。
	// 从最终数据，解析出每一个Key和其对应的一组Value切片，将其传递给Reduce函数。
	// 并将Reduce函数的输出序列化为JSON格式后写入到结果文件。
	tmps := make([]KeyValueList, nMap)
	var data KeyValueList
	for i := 0; i < nMap; i++ {
		inFile := reduceName(jobName, i, reduceTask)
		f, openErr := os.Open(inFile)
		if openErr != nil {
			log.Fatal("Open intermedia file: ", openErr)
		}
		defer f.Close()
		dec := json.NewDecoder(f)

		// 从map的每个输出文件逐个反序列出KeyValue对象，直到遇到EOF出错为止。
		for {
			var kv KeyValue
			err := dec.Decode(&kv)
			if err != nil {
				//fmt.Println("[Debug]Decode map outFile: ", err)
				break
			}
			tmps[i] = append(tmps[i], kv)
		}

		/* === debug === */
			//tmpf, err := os.OpenFile("reduce-sort-tmp-"+strconv.Itoa(i)+".json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			//if err != nil {
			//	log.Fatal("Open reduce-sort-tmp: ", err)
			//}
			//defer tmpf.Close()
			//tmpEncErr := json.NewEncoder(tmpf).Encode(&tmps[i])
			//if tmpEncErr != nil {
			//	log.Fatal("Encode reduce-sort-tmp: ", tmpEncErr)
			//}
		/* === === */

		sort.Sort(tmps[i])

		data = append(data, tmps[i]...)
	}
	sort.Sort(data)

	/* === debug === */
		//dataf, err := os.OpenFile("data-sort.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
		//if err != nil {
		//	log.Fatal("Open data-sort: ", err)
		//}
		//defer dataf.Close()
		//dataEncErr := json.NewEncoder(dataf).Encode(&data)
		//if dataEncErr != nil {
		//	log.Fatal("Encode data-sort: ", dataEncErr)
		//}
	/* === === */


	f, openErr := os.OpenFile(outFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
	if openErr != nil {
		log.Fatal("Open result file: ", openErr)
	}
	enc := json.NewEncoder(f)

	// 从合并了M个map输出并排序的数据中解析中每个Key和其对应的一组Value，传递给Reduce函数。
	// 将该Key和Reduce输出的Value作为结果Key/Value对，序列化为JSON后逐个写入到结果文件。
	begin := 0
	for ;begin < len(data); {
		end :=begin
		for ; end < len(data) && data[end].Key == data[begin].Key; end++ {

		}
		key := data[begin].Key
		var value []string
		for i := begin; i < end; i++ {
			value = append(value, data[i].Value)
		}
		err := enc.Encode(KeyValue{key, reduceF(key, value)})
		if err != nil {
			log.Fatal("Encode result file: ", err)
		}
		begin = end
	}
	f.Close()
}
