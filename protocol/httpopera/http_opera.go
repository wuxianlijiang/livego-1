package httpopera

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/livego/av"
	"github.com/livego/concurrent-map"
	"github.com/livego/configure"
	log "github.com/livego/logging"
	"github.com/livego/protocol/rtmp"
	"github.com/livego/protocol/rtmp/rtmprelay"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Response struct {
	w       http.ResponseWriter
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (r *Response) SendJson() (int, error) {
	resp, _ := json.Marshal(r)
	r.w.Header().Set("Content-Type", "application/json")
	return r.w.Write(resp)
}

type Operation struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Stop   bool   `json:"stop"`
}

type OperationChange struct {
	Method    string `json:"method"`
	SourceURL string `json:"source_url"`
	TargetURL string `json:"target_url"`
	Stop      bool   `json:"stop"`
}

type ClientInfo struct {
	url              string
	rtmpRemoteClient *rtmp.Client
	rtmpLocalClient  *rtmp.Client
}

type Server struct {
	handler       av.Handler
	session       map[string]*rtmprelay.RtmpRelay
	sessionFlv    map[string]*rtmprelay.FlvPull
	sessionMRelay cmap.ConcurrentMap //map[string]*rtmprelay.MultipleReley, key为instanceid
	mrelayMutex   sync.RWMutex
	rtmpAddr      string
}

func NewServer(h av.Handler, rtmpAddr string) *Server {
	return &Server{
		handler:       h,
		session:       make(map[string]*rtmprelay.RtmpRelay),
		sessionFlv:    make(map[string]*rtmprelay.FlvPull),
		sessionMRelay: cmap.New(),
		rtmpAddr:      rtmpAddr,
	}
}

type ReportStat struct {
	serverList  []string
	isStart     bool
	localServer *Server
}

type MRelayStart struct {
	Instancename string
	Dsturl       string
	Srcurlset    []rtmprelay.SrcUrlItem
	Buffertime   int
}

type MRelayAdd struct {
	Instanceid int64
	Srcurlset  []rtmprelay.SrcUrlItem
	Buffertime int
}
type MRelayReponse struct {
	Retcode      int
	Instanceid   int64
	Instancename string
	Dscr         string
}

var reportStatObj *ReportStat

func (s *Server) Serve(l net.Listener) error {
	mux := http.NewServeMux()

	mux.Handle("/statics", http.FileServer(http.Dir("statics")))

	mux.HandleFunc("/control/push", func(w http.ResponseWriter, r *http.Request) {
		s.handlePush(w, r)
	})
	mux.HandleFunc("/control/pull", func(w http.ResponseWriter, r *http.Request) {
		s.handlePull(w, r)
	})
	mux.HandleFunc("/control/pullflv", func(w http.ResponseWriter, r *http.Request) {
		s.handlePullflv(w, r)
	})
	mux.HandleFunc("/control/multiplerelay/start", func(w http.ResponseWriter, r *http.Request) {
		s.handleMultipleRelayStart(w, r)
	})
	mux.HandleFunc("/control/multiplerelay/stop", func(w http.ResponseWriter, r *http.Request) {
		s.handleMultipleRelayStop(w, r)
	})
	mux.HandleFunc("/control/multiplerelay/set", func(w http.ResponseWriter, r *http.Request) {
		s.handleMultipleRelaySet(w, r)
	})
	mux.HandleFunc("/control/multiplerelay/add", func(w http.ResponseWriter, r *http.Request) {
		s.handleMultipleRelayAdd(w, r)
	})
	mux.HandleFunc("/control/multiplerelay/remove", func(w http.ResponseWriter, r *http.Request) {
		s.handleMultipleRelayRemove(w, r)
	})

	mux.HandleFunc("/stat/livestat", func(w http.ResponseWriter, r *http.Request) {
		s.GetLiveStatics(w, r)
	})

	reportStatObj = NewReportStat(configure.GetReportList(), s)
	err := reportStatObj.Start()
	if err != nil {
		log.Error("ReportStat start error:", err)
		return err
	}
	defer reportStatObj.Stop()

	http.Serve(l, mux)
	return nil
}

type Stream struct {
	Key             string `json:"key"`
	Url             string `json:"Url"`
	PeerIP          string `json:"PeerIP"`
	StreamId        uint32 `json:"StreamId"`
	VideoTotalBytes uint64 `json:123456`
	VideoSpeed      uint64 `json:123456`
	AudioTotalBytes uint64 `json:123456`
	AudioSpeed      uint64 `json:123456`
}

type Streams struct {
	PublisherNumber int64
	PlayerNumber    int64
	Publishers      []Stream `json:"publishers"`
	Players         []Stream `json:"players"`
}

//http://127.0.0.1:8070/stat/livestat
func (server *Server) GetLiveStatics(w http.ResponseWriter, req *http.Request) {
	rtmpStream := server.handler.(*rtmp.RtmpStream)
	if rtmpStream == nil {
		io.WriteString(w, "<h1>Get rtmp Stream information error</h1>")
		return
	}

	msgs := new(Streams)
	msgs.PublisherNumber = 0
	msgs.PlayerNumber = 0
	for item := range rtmpStream.GetStreams().IterBuffered() {
		if s, ok := item.Val.(*rtmp.Stream); ok {
			if s.GetReader() != nil {
				switch s.GetReader().(type) {
				case *rtmp.VirReader:
					v := s.GetReader().(*rtmp.VirReader)
					msg := Stream{item.Key, v.Info().URL, v.ReadBWInfo.PeerIP, v.ReadBWInfo.StreamId, v.ReadBWInfo.VideoDatainBytes, v.ReadBWInfo.VideoSpeedInBytesperMS,
						v.ReadBWInfo.AudioDatainBytes, v.ReadBWInfo.AudioSpeedInBytesperMS}
					msgs.Publishers = append(msgs.Publishers, msg)
					msgs.PublisherNumber++
				}
			}
		}
	}

	for item := range rtmpStream.GetStreams().IterBuffered() {
		ws := item.Val.(*rtmp.Stream).GetWs()
		for s := range ws.IterBuffered() {
			if pw, ok := s.Val.(*rtmp.PackWriterCloser); ok {
				if pw.GetWriter() != nil {
					switch pw.GetWriter().(type) {
					case *rtmp.VirWriter:
						v := pw.GetWriter().(*rtmp.VirWriter)
						msg := Stream{item.Key, v.Info().URL, v.WriteBWInfo.PeerIP, v.WriteBWInfo.StreamId, v.WriteBWInfo.VideoDatainBytes, v.WriteBWInfo.VideoSpeedInBytesperMS,
							v.WriteBWInfo.AudioDatainBytes, v.WriteBWInfo.AudioSpeedInBytesperMS}
						msgs.Players = append(msgs.Players, msg)
						msgs.PlayerNumber++
					}
				}
			}
		}
	}
	resp, _ := json.Marshal(msgs)
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (self *Server) multipleRelayResponse(w http.ResponseWriter, retCode int, id int64, name string, dscr string) {
	respData := &MRelayReponse{
		Retcode:      retCode,
		Instanceid:   id,
		Instancename: name,
		Dscr:         dscr,
	}
	data, _ := json.Marshal(respData)
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (self *Server) createMutipleRelayId() int64 {
	var id int64
	for {
		nowTimestamp := time.Now().UnixNano() / 1000 / 1000 / 1000
		retNumber := rand.Intn(1000)

		id = nowTimestamp*1000 + int64(retNumber)
		idString := fmt.Sprintf("%d", id)

		self.mrelayMutex.RLock()
		ret := self.sessionMRelay.Has(idString)
		self.mrelayMutex.RUnlock()
		if !ret {
			break
		}
	}
	return id
}

/*
{"instancename":"xxxxx", "dsturl":"rtmp://qqpush.inke.cn/live/streams",
"srcurlset":[{"srcid":"1", "srcurl":"rtmp://xxxx/live/src1"}, {"srcid":"1", "srcurl":"rtmp://xxxx/live/src1"}],
"buffertime":2}
{"instanceid":"123456", "srcurlset":[{"srcid":"1", "srcurl":"rtmp://xxxx/live/src1"}, {"srcid":"1", "srcurl":"rtmp://xxxx/live/src1"}],
"buffertime":2}
*/
/*
{"recode":0, "instanceid":"123456", "instancename":"xxxx", "dscr":"xxxxx"}
*/
//http://127.0.0.1:8070/control/multiplerelay?oper=start&srcindex=1&dsturl=http://pull.inke.cn/live/1503042379094575.flv&srcurl=rtmp://
func (self *Server) handleMultipleRelayStart(w http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Error("http body error", err)
		self.multipleRelayResponse(w, -1, 0, "", "start multiple relay read data error")
	} else {
		log.Info("response:", string(body))
		var mrelayinfo MRelayStart

		err = json.Unmarshal(body, &mrelayinfo)
		if err != nil {
			log.Error("http body json decode error:", err)
			self.multipleRelayResponse(w, -1, 0, "", "start multiple relay json decode error")
		} else {
			log.Info("multiple relay start:", mrelayinfo)
			id := self.createMutipleRelayId()

			mRelay := rtmprelay.NewMultipleReley(id, mrelayinfo.Instancename,
				mrelayinfo.Dsturl, mrelayinfo.Srcurlset, mrelayinfo.Buffertime)
			err := mRelay.Start()
			if err == nil {
				self.multipleRelayResponse(w, 0, id, mrelayinfo.Instancename,
					fmt.Sprintf("start multiple relay name=%s id=%d ok", mrelayinfo.Instancename, id))
				idString := fmt.Sprintf("%d", id)
				self.mrelayMutex.Lock()
				self.sessionMRelay.Set(idString, mRelay)
				self.mrelayMutex.Unlock()
			} else {
				self.multipleRelayResponse(w, -1, 0, mrelayinfo.Instancename,
					fmt.Sprintf("start multiple relay name=%s error", mrelayinfo.Instancename))
			}
		}

	}
}

func (self *Server) handleMultipleRelayStop(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	idArray := req.Form["instanceid"]
	if idArray == nil || len(idArray) <= 0 {
		self.multipleRelayResponse(w, -1, 0, "", "stop multiple relay parameter instanceid error")
		return
	}
	idString := idArray[0]
	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil {
		self.multipleRelayResponse(w, -1, 0, "", "stop multiple relay parameter instanceid error")
		return
	}
	mRelay, ret := self.sessionMRelay.Get(idString)
	if !ret {
		self.multipleRelayResponse(w, -1, 0, "",
			fmt.Sprintf("stop multiple relay parameter instanceid error: id(%s) doesn't exist.", idString))
		return
	}
	mRelay.(*rtmprelay.MultipleReley).Stop()

	self.mrelayMutex.Lock()
	self.sessionMRelay.Remove(idString)
	self.mrelayMutex.Unlock()

	self.multipleRelayResponse(w, 0, id, mRelay.(*rtmprelay.MultipleReley).Instancename,
		fmt.Sprintf("stop id(%s) multiple relay ok", idString))
}

func (self *Server) handleMultipleRelaySet(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	idArray := req.Form["instanceid"]
	srcIndexArray := req.Form["srcindex"]

	if (idArray == nil || len(idArray) <= 0) || (srcIndexArray == nil || len(srcIndexArray) <= 0) {
		self.multipleRelayResponse(w, -1, 0, "", "set multiple relay parameter error")
		return
	}
	idString := idArray[0]
	srcindexString := srcIndexArray[0]

	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil {
		self.multipleRelayResponse(w, -1, 0, "", "set multiple relay parameter error")
		return
	}

	srcindex, err := strconv.Atoi(srcindexString)
	if err != nil {
		self.multipleRelayResponse(w, -1, id, "", "set multiple relay parameter error")
		return
	}

	mRelay, ret := self.sessionMRelay.Get(idString)
	if !ret {
		self.multipleRelayResponse(w, -1, id, "",
			fmt.Sprintf("set multiple relay parameter error: id(%s) doesn't exist.", idString))
		return
	}
	err = mRelay.(*rtmprelay.MultipleReley).SetActiveSrcUrl(srcindex)
	if err != nil {
		self.multipleRelayResponse(w, -1, id, "",
			fmt.Sprintf("set multiple relay SetActiveSrcUrl error:%v", err))
		return
	}
	self.multipleRelayResponse(w, 0, id, mRelay.(*rtmprelay.MultipleReley).Instancename,
		fmt.Sprintf("set id(%s) multiple relay ok", idString))
}

func (self *Server) handleMultipleRelayRemove(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()

	idArray := req.Form["instanceid"]
	srcIndexArray := req.Form["srcindex"]

	if (idArray == nil || len(idArray) <= 0) || (srcIndexArray == nil || len(srcIndexArray) <= 0) {
		self.multipleRelayResponse(w, -1, 0, "", "remove multiple relay parameter error")
		return
	}
	idString := idArray[0]
	srcindexString := srcIndexArray[0]

	id, err := strconv.ParseInt(idString, 10, 64)
	if err != nil {
		self.multipleRelayResponse(w, -1, 0, "", "remove multiple relay parameter error")
		return
	}

	srcindexs := strings.Split(srcindexString, ",")

	if srcindexs == nil && len(srcindexs) == 0 {
		self.multipleRelayResponse(w, -1, 0, "", "remove multiple relay parameter error")
		return
	}

	var srcidArray []int

	for _, srcidString := range srcindexs {
		srcindex, err := strconv.Atoi(srcidString)
		if err != nil {
			self.multipleRelayResponse(w, -1, id, "", "remove multiple relay parameter error")
			return
		}
		srcidArray = append(srcidArray, srcindex)
	}

	mRelay, ret := self.sessionMRelay.Get(idString)
	if !ret {
		self.multipleRelayResponse(w, -1, id, "",
			fmt.Sprintf("remove multiple relay parameter error: id(%s) doesn't exist.", idString))
		return
	}

	mRelay.(*rtmprelay.MultipleReley).RemoveSrcArray(srcidArray)

	self.multipleRelayResponse(w, 0, id, mRelay.(*rtmprelay.MultipleReley).Instancename,
		"remove multiple relay ok")
}

func (self *Server) handleMultipleRelayAdd(w http.ResponseWriter, req *http.Request) {
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		log.Error("http body error", err)
		self.multipleRelayResponse(w, -1, 0, "", "Add multiple relay read data error")
		return
	}

	log.Info("response:", string(body))
	var mrelayinfo MRelayAdd

	err = json.Unmarshal(body, &mrelayinfo)
	if err != nil {
		log.Error("http body json decode error:", err)
		self.multipleRelayResponse(w, -1, 0, "", "Add multiple relay json decode error")
		return
	}
	log.Info("multiple relay add:", mrelayinfo)
	idString := fmt.Sprintf("%d", mrelayinfo.Instanceid)

	ret := self.sessionMRelay.Has(idString)
	if !ret {
		log.Errorf("multiple add error:id(%d) not found", mrelayinfo.Instanceid)
		self.multipleRelayResponse(w, -1, mrelayinfo.Instanceid, "", "multiple add error:id not found")
		return
	}

	mRelay, ret := self.sessionMRelay.Get(idString)
	if !ret {
		log.Errorf("multiple add error:id(%d) not found", mrelayinfo.Instanceid)
		self.multipleRelayResponse(w, -1, mrelayinfo.Instanceid, "", "multiple add error:id not found")
		return
	}
	mRelay.(*rtmprelay.MultipleReley).AddSrcArray(mrelayinfo.Srcurlset)
	self.multipleRelayResponse(w, 0, mrelayinfo.Instanceid,
		mRelay.(*rtmprelay.MultipleReley).Instancename, "multiple add ok")
}

//http://127.0.0.1:8070/control/pullflv?oper=start&app=live&name=stream1&url=http://qqpull.inke.cn/live/stream1.flv
func (s *Server) handlePullflv(w http.ResponseWriter, req *http.Request) {
	var retString string
	var err error

	req.ParseForm()

	oper := req.Form["oper"]
	app := req.Form["app"]
	name := req.Form["name"]
	url := req.Form["url"]

	log.Infof("control pullflv: oper=%v, app=%v, name=%v, url=%v", oper, app, name, url)
	if (len(app) <= 0) || (len(name) <= 0) || (len(url) <= 0) {
		io.WriteString(w, "control push parameter error, please check them.</br>")
		return
	}

	remoteurl := "rtmp://127.0.0.1" + s.rtmpAddr + "/" + app[0] + "/" + name[0]
	localurl := url[0]

	keyString := "pull:" + app[0] + "/" + name[0]
	if oper[0] == "stop" {
		pullFlvrelay, found := s.sessionFlv[keyString]

		if !found {
			retString = fmt.Sprintf("sessionFlv key[%s] not exist, please check it again.", keyString)
			io.WriteString(w, retString)
			return
		}
		log.Infof("flvpull stop push %s from %s", remoteurl, localurl)
		pullFlvrelay.Stop()

		delete(s.sessionFlv, keyString)
		retString = fmt.Sprintf("<h1>push url stop %s ok</h1></br>", url[0])
		io.WriteString(w, retString)
		log.Infof("flvpull stop return %s", retString)
	} else {
		pullFlvrelay := rtmprelay.NewFlvPull(&localurl, &remoteurl)
		log.Infof("flvpull start push %s from %s", remoteurl, localurl)
		err = pullFlvrelay.Start()
		if err != nil {
			retString = fmt.Sprintf("push error=%v", err)
		} else {
			s.sessionFlv[keyString] = pullFlvrelay
			retString = fmt.Sprintf("<h1>push url start %s ok</h1></br>", url[0])
		}
		io.WriteString(w, retString)
		log.Infof("flvpull start return %s", retString)
	}
}

//http://127.0.0.1:8070/control/pull?oper=start&app=live&name=stream1&url=rtmp://pull.inke.cn/live/stream1
func (s *Server) handlePull(w http.ResponseWriter, req *http.Request) {
	var retString string
	var err error

	req.ParseForm()

	oper := req.Form["oper"]
	app := req.Form["app"]
	name := req.Form["name"]
	url := req.Form["url"]

	log.Infof("control pull: oper=%v, app=%v, name=%v, url=%v", oper, app, name, url)
	if (len(app) <= 0) || (len(name) <= 0) || (len(url) <= 0) {
		io.WriteString(w, "control push parameter error, please check them.</br>")
		return
	}

	remoteurl := "rtmp://127.0.0.1" + s.rtmpAddr + "/" + app[0] + "/" + name[0]
	localurl := url[0]

	keyString := "pull:" + app[0] + "/" + name[0]
	if oper[0] == "stop" {
		pullRtmprelay, found := s.session[keyString]

		if !found {
			retString = fmt.Sprintf("session key[%s] not exist, please check it again.", keyString)
			io.WriteString(w, retString)
			return
		}
		log.Infof("rtmprelay stop push %s from %s", remoteurl, localurl)
		pullRtmprelay.Stop()

		delete(s.session, keyString)
		retString = fmt.Sprintf("<h1>push url stop %s ok</h1></br>", url[0])
		io.WriteString(w, retString)
		log.Infof("pull stop return %s", retString)
	} else {
		pullRtmprelay := rtmprelay.NewRtmpRelay(&localurl, &remoteurl)
		log.Infof("rtmprelay start push %s from %s", remoteurl, localurl)
		err = pullRtmprelay.Start()
		if err != nil {
			retString = fmt.Sprintf("push error=%v", err)
		} else {
			s.session[keyString] = pullRtmprelay
			retString = fmt.Sprintf("<h1>push url start %s ok</h1></br>", url[0])
		}
		io.WriteString(w, retString)
		log.Infof("pull start return %s", retString)
	}
}

//http://127.0.0.1:8090/control/push?&oper=start&app=live&name=123456&url=rtmp://192.168.16.136/live/123456
func (s *Server) handlePush(w http.ResponseWriter, req *http.Request) {
	var retString string
	var err error

	req.ParseForm()

	oper := req.Form["oper"]
	app := req.Form["app"]
	name := req.Form["name"]
	url := req.Form["url"]

	log.Infof("control push: oper=%v, app=%v, name=%v, url=%v", oper, app, name, url)
	if (len(app) <= 0) || (len(name) <= 0) || (len(url) <= 0) {
		io.WriteString(w, "control push parameter error, please check them.</br>")
		return
	}

	localurl := "rtmp://127.0.0.1" + s.rtmpAddr + "/" + app[0] + "/" + name[0]
	remoteurl := url[0]

	keyString := "push:" + app[0] + "/" + name[0]
	if oper[0] == "stop" {
		pushRtmprelay, found := s.session[keyString]
		if !found {
			retString = fmt.Sprintf("<h1>session key[%s] not exist, please check it again.</h1>", keyString)
			io.WriteString(w, retString)
			return
		}
		log.Infof("rtmprelay stop push %s from %s", remoteurl, localurl)
		pushRtmprelay.Stop()

		delete(s.session, keyString)
		retString = fmt.Sprintf("<h1>push url stop %s ok</h1></br>", url[0])
		io.WriteString(w, retString)
		log.Infof("push stop return %s", retString)
	} else {
		pushRtmprelay := rtmprelay.NewRtmpRelay(&localurl, &remoteurl)
		log.Infof("rtmprelay start push %s from %s", remoteurl, localurl)
		err = pushRtmprelay.Start()
		if err != nil {
			retString = fmt.Sprintf("push error=%v", err)
		} else {
			retString = fmt.Sprintf("<h1>push url start %s ok</h1></br>", url[0])
			s.session[keyString] = pushRtmprelay
		}

		io.WriteString(w, retString)
		log.Infof("push start return %s", retString)
	}
}

func NewReportStat(serverlist []string, localserver *Server) *ReportStat {
	return &ReportStat{
		serverList:  serverlist,
		isStart:     false,
		localServer: localserver,
	}
}

func (self *ReportStat) httpsend(data []byte) error {

	return nil
}

func (self *ReportStat) onWork() {
	rtmpStream := self.localServer.handler.(*rtmp.RtmpStream)
	if rtmpStream == nil {
		return
	}
	for {
		if !self.isStart {
			break
		}

		if self.serverList == nil || len(self.serverList) == 0 {
			log.Warning("Report statics server list is null.")
			break
		}

		msgs := new(Streams)
		msgs.PublisherNumber = 0
		msgs.PlayerNumber = 0

		for item := range rtmpStream.GetStreams().IterBuffered() {
			if s, ok := item.Val.(*rtmp.Stream); ok {
				if s.GetReader() != nil {
					switch s.GetReader().(type) {
					case *rtmp.VirReader:
						v := s.GetReader().(*rtmp.VirReader)
						msg := Stream{item.Key, v.Info().URL, v.ReadBWInfo.PeerIP, v.ReadBWInfo.StreamId, v.ReadBWInfo.VideoDatainBytes, v.ReadBWInfo.VideoSpeedInBytesperMS,
							v.ReadBWInfo.AudioDatainBytes, v.ReadBWInfo.AudioSpeedInBytesperMS}
						msgs.Publishers = append(msgs.Publishers, msg)
						msgs.PublisherNumber++
					}
				}
			}
		}

		for item := range rtmpStream.GetStreams().IterBuffered() {
			ws := item.Val.(*rtmp.Stream).GetWs()
			for s := range ws.IterBuffered() {
				if pw, ok := s.Val.(*rtmp.PackWriterCloser); ok {
					if pw.GetWriter() != nil {
						switch pw.GetWriter().(type) {
						case *rtmp.VirWriter:
							v := pw.GetWriter().(*rtmp.VirWriter)
							msg := Stream{item.Key, v.Info().URL, v.WriteBWInfo.PeerIP, v.WriteBWInfo.StreamId, v.WriteBWInfo.VideoDatainBytes, v.WriteBWInfo.VideoSpeedInBytesperMS,
								v.WriteBWInfo.AudioDatainBytes, v.WriteBWInfo.AudioSpeedInBytesperMS}
							msgs.Players = append(msgs.Players, msg)
							msgs.PlayerNumber++
						}
					}
				}
			}
		}
		resp, _ := json.Marshal(msgs)

		//log.Info("report statics server list:", self.serverList)
		//log.Info("resp:", string(resp))

		self.httpsend(resp)
		time.Sleep(time.Second * 5)
	}
}

func (self *ReportStat) Start() error {
	if self.isStart {
		return errors.New("Report Statics has already started.")
	}

	self.isStart = true

	go self.onWork()
	return nil
}

func (self *ReportStat) Stop() {
	if !self.isStart {
		return
	}

	self.isStart = false
}
