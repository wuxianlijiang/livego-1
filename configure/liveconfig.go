package configure

import (
	"encoding/json"
	"fmt"
	log "github.com/livego/logging"
	"io/ioutil"
	"strings"
)

/*
{
    "listen": 1935,
    "hls" : "enable"
    "hlsport" : 80
    "httpflv" : "enable"
    "flvport" : 80
    "httpoper": "enable"
    "operport": 80
    "servers":[
        {
        "servername":"live",
        "exec_push":["./helloworld1", "./helloworld2"],
        "exec_push_done":["./helloworld1", "./helloworld2"],
        "report":["127.0.0.1"],
	    "static_push":[{"master_prefix":"live/trans/inke/mlinkm", "upstream":"rtmp://inke.8686c.com/"}],
	    "static_pull":[{"type":"http-flv",
	                    "source":"http://pull99.a8.com/live/1500365043587794.flv",
	                    "app":"live",
	                    "stream":"1500365043587794"},
	                    {"type":"rtmp",
	                    "source":"rtmp://pull99.a8.com/live/1500365043587794",
	                    "app":"live",
	                    "stream":"1500365043587794"}
	                  ],
	    "sub_static_push":[{"master_prefix":"live/trans/inke/mlinkm", "sub_prefix":"live/trans/inke/mlinks"}]
        }
    ]
}
*/
type SubStaticPush struct {
	Master_prefix string
	Sub_prefix    string
}

type StaticPushInfo struct {
	Master_prefix string
	Upstream      string
}

type StaticPullInfo struct {
	Type   string
	Source string
	App    string
	Stream string
}

type ServerInfo struct {
	Servername      string
	Exec_push       []string
	Exec_push_done  []string
	Report          []string
	Static_push     []StaticPushInfo
	Static_pull     []StaticPullInfo
	Sub_static_push []SubStaticPush
}

type ServerCfg struct {
	Listen    int
	Hls       string
	Hlsport   int
	Httpflv   string
	Flvport   int
	Httpoper  string
	Operport  int
	Chunksize int
	Servers   []ServerInfo
}

var RtmpServercfg ServerCfg

var isStaticPushEnable bool
var isSubStaticPushEnable bool

func LoadConfig(configfilename string) error {
	log.Infof("starting load configure file(%s)......", configfilename)
	data, err := ioutil.ReadFile(configfilename)
	if err != nil {
		log.Errorf("ReadFile %s error:%v", configfilename, err)
		return err
	}

	log.Infof("loadconfig: \r\n%s", string(data))

	err = json.Unmarshal(data, &RtmpServercfg)
	if err != nil {
		log.Errorf("json.Unmarshal error:%v", err)
		return err
	}
	log.Infof("get config json data:%v", RtmpServercfg)

	if RtmpServercfg.Chunksize == 0 {
		RtmpServercfg.Chunksize = 4096
	}

	log.Warning("Chunk size:", RtmpServercfg.Chunksize)

	isStaticPushEnable = false
	isSubStaticPushEnable = false
	for _, serverItem := range RtmpServercfg.Servers {
		if serverItem.Static_push != nil && len(serverItem.Static_push) > 0 {
			isStaticPushEnable = true
		}
		if serverItem.Sub_static_push != nil && len(serverItem.Sub_static_push) > 0 {
			isSubStaticPushEnable = true
		}
	}

	return nil
}

func GetReportList() []string {
	var reportlist []string

	for _, serverItem := range RtmpServercfg.Servers {
		reportlist = append(reportlist, serverItem.Report...)
	}

	return reportlist
}

func GetExecPush() []string {
	var execList []string

	for _, serverItem := range RtmpServercfg.Servers {
		for _, item := range serverItem.Exec_push {
			execList = append(execList, item)
		}
	}
	return execList
}

func GetExecPushDone() []string {
	var execList []string

	for _, serverItem := range RtmpServercfg.Servers {
		for _, item := range serverItem.Exec_push_done {
			execList = append(execList, item)
		}
	}
	return execList
}

func GetChunkSize() int {
	return RtmpServercfg.Chunksize
}

func IsHttpOperEnable() bool {
	httpOper := strings.ToLower(RtmpServercfg.Httpoper)
	//log.Warning("http operation", httpOper)
	if httpOper == "enable" {
		return true
	}
	return false
}

func IsHttpFlvEnable() bool {
	flv := strings.ToLower(RtmpServercfg.Httpflv)
	//log.Warning("http-flv", flv)
	if flv == "enable" {
		return true
	}
	return false
}

func IsHlsEnable() bool {
	hls := strings.ToLower(RtmpServercfg.Hls)
	//log.Warning("HLS", hls)
	if hls == "enable" {
		return true
	}

	return false
}

func GetListenPort() int {
	return RtmpServercfg.Listen
}

func GetHlsPort() int {
	return RtmpServercfg.Hlsport
}

func GetHttpFlvPort() int {
	return RtmpServercfg.Flvport
}

func GetHttpOperPort() int {
	return RtmpServercfg.Operport
}

func GetStaticPullList() (pullInfoList []StaticPullInfo, bRet bool) {
	pullInfoList = nil
	bRet = false

	for _, serverinfo := range RtmpServercfg.Servers {
		if serverinfo.Static_pull != nil && len(serverinfo.Static_pull) > 0 {
			bRet = true
			pullInfoList = append(pullInfoList, serverinfo.Static_pull[:]...)
		}
	}

	return
}

func GetStaticPushUrlList(rtmpurl string) (retArray []string, bRet bool) {
	if !isStaticPushEnable {
		return nil, false
	}

	retArray = nil
	bRet = false

	//log.Printf("rtmpurl=%s", rtmpurl)
	url := rtmpurl[7:]

	index := strings.Index(url, "/")
	if index <= 0 {
		return
	}
	url = url[index+1:]
	//log.Printf("GetStaticPushUrlList: url=%s", url)
	for _, serverinfo := range RtmpServercfg.Servers {
		//log.Printf("server info:%v", serverinfo)
		for _, staticpushItem := range serverinfo.Static_push {
			masterPrefix := staticpushItem.Master_prefix
			upstream := staticpushItem.Upstream
			//log.Printf("push item: masterprefix=%s, upstream=%s", masterPrefix, upstream)
			if strings.Contains(url, masterPrefix) {
				newUrl := ""
				index := strings.Index(url, "/")
				if index <= 0 {
					newUrl = url
				} else {
					newUrl = url[index+1:]
				}
				destUrl := fmt.Sprintf("%s/%s", upstream, newUrl)
				retArray = append(retArray, destUrl)
				bRet = true
			}
		}
	}

	//log.Printf("GetStaticPushUrlList:%v, %v", retArray, bRet)
	return
}

func GetSubStaticMasterPushUrl(rtmpurl string) (retUpstream string, bRet bool) {
	if !isSubStaticPushEnable {
		return "", false
	}

	retUpstream = ""
	bRet = false

	url := rtmpurl[7:]

	index := strings.Index(url, "/")
	if index <= 0 {
		return
	}
	url = url[index+1:]

	bFoundFlag := false
	foundMasterPrefix := ""
	for _, serverinfo := range RtmpServercfg.Servers {
		for _, substaticpushItem := range serverinfo.Sub_static_push {
			masterPrefix := substaticpushItem.Master_prefix
			subPrefix := substaticpushItem.Sub_prefix
			if strings.Contains(url, subPrefix) {
				foundMasterPrefix = masterPrefix
				bFoundFlag = true
				break
			}
		}

		if bFoundFlag {
			for _, staticpushItem := range serverinfo.Static_push {
				masterPrefix := staticpushItem.Master_prefix
				upstream := staticpushItem.Upstream
				if foundMasterPrefix == masterPrefix {
					newPrefix := ""
					index := strings.Index(masterPrefix, "/")
					if index <= 0 {
						newPrefix = masterPrefix
					} else {
						newPrefix = masterPrefix[index+1:]
					}
					retUpstream = fmt.Sprintf("%s/%s", upstream, newPrefix)
					bRet = true
					return
				}
			}
			break
		}
	}

	return
}
