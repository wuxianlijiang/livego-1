package rtmprelay

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"github.com/livego/concurrent-map"
	log "github.com/livego/logging"
	"github.com/livego/protocol/amf"
	"github.com/livego/protocol/rtmp/core"
	"io"
	"strings"
	"sync"
	"time"
)

var BUFFER_DEF_SIZE = 40

type SrcUrlItem struct {
	Srcid  int
	Srcurl string
}

type MultipleReley struct {
	Id                    int64
	Instancename          string
	dstUrl                string
	srcUrls               []SrcUrlItem
	pulllistRunningFlag   bool
	pulllistEndFlag       bool
	mutexSrcUrls          sync.RWMutex
	rtmpRelays            cmap.ConcurrentMap //map[string]*RtmpRelay
	flvRelays             cmap.ConcurrentMap //map[string]*FlvPull
	playId                int
	isLocalPlayStart      bool
	mutexIsLocalplayStart sync.RWMutex
	mutexLocalplay        sync.RWMutex
	mutexRemotepush       sync.RWMutex
	localplay             *core.ConnClient
	remotepush            *core.ConnClient
	cs_chan               chan *core.ChunkStream
	isStart               bool
	mutexIsStart          sync.RWMutex
	chunkList             *list.List
	listBufferSize        int
	listBufferMax         int
	listBufferInit        bool
	mutexList             sync.RWMutex
}

func NewMultipleReley(id int64, name string, dsrUrl string, srcUrls []SrcUrlItem, buffertime int) *MultipleReley {
	if buffertime <= 0 {
		buffertime = 1
	}
	return &MultipleReley{
		Id:             id,
		Instancename:   name,
		dstUrl:         dsrUrl,
		srcUrls:        srcUrls,
		rtmpRelays:     cmap.New(), //map[string]*RtmpRelay
		flvRelays:      cmap.New(), //map[string]*FlvPull
		isStart:        false,
		chunkList:      list.New(),
		listBufferSize: BUFFER_DEF_SIZE * buffertime,
		listBufferMax:  BUFFER_DEF_SIZE*buffertime + 20,
		listBufferInit: false,
	}
}

func (self *MultipleReley) AddSrcArray(srcitems []SrcUrlItem) {
	self.pulllistRunningFlag = false

	for {
		if self.pulllistEndFlag {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	self.mutexSrcUrls.Lock()
	self.srcUrls = append(self.srcUrls, srcitems...)
	self.mutexSrcUrls.Unlock()

	self.pulllistRunningFlag = true
	go self.pulllistStart()
}

func (self *MultipleReley) RemoveSrcArray(srcIdArray []int) {
	self.pulllistRunningFlag = false

	for {
		if self.pulllistEndFlag {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	var delitems []SrcUrlItem

	self.mutexSrcUrls.Lock()
	for _, srcid := range srcIdArray {
		var index int
		var item SrcUrlItem
		bFound := false
		for index, item = range self.srcUrls {
			if item.Srcid == srcid {
				bFound = true
				delitems = append(delitems, item)
				break
			}
		}
		if bFound {
			self.srcUrls = append(self.srcUrls[:index], self.srcUrls[index+1:]...)
		}
	}
	log.Warningf("RemoveSrcArray: srsurls=%v", self.srcUrls)
	self.mutexSrcUrls.Unlock()

	self.pulllistRemoveStop(delitems)
}

func (self *MultipleReley) GetKeyString(srcUrl string) (string, error) {
	tempString := srcUrl[7:]
	ipos := strings.Index(tempString, "/")
	if ipos < 0 {
		return "", errors.New(fmt.Sprintf("GetKeyString error:%s", srcUrl))
	}

	retString := tempString[ipos+1:]

	if srcUrl[0:4] == "http" {
		retString = retString[0 : len(retString)-4]
	}
	return retString, nil
}

func (self *MultipleReley) pulllistStop() {
	var flvUrls []string
	var rtmpUrls []string

	for item := range self.flvRelays.IterBuffered() {
		url := item.Key
		flvUrls = append(flvUrls, url)
		flvpull := item.Val.(*FlvPull)
		flvpull.Stop()
	}

	for _, url := range flvUrls {
		self.flvRelays.Remove(url)
	}

	for item := range self.rtmpRelays.IterBuffered() {
		url := item.Key
		rtmpUrls = append(rtmpUrls, url)
		rtmprelay := item.Val.(*RtmpRelay)
		rtmprelay.Stop()
	}

	for _, url := range rtmpUrls {
		self.rtmpRelays.Remove(url)
	}
}

func (self *MultipleReley) pulllistRemoveStop(delitems []SrcUrlItem) {
	self.mutexSrcUrls.RLock()
	for _, item := range delitems {
		srcUrl := item.Srcurl
		headerString := srcUrl[0:4]

		if headerString == "http" {
			httpRelay, ret := self.flvRelays.Get(srcUrl)
			if ret {
				log.Warningf("flv pull has been removed srcurl=%s", srcUrl)
				httpRelay.(*FlvPull).Stop()
			}
		} else if headerString == "rtmp" {
			rtmpClient, ret := self.rtmpRelays.Get(srcUrl)
			if ret {
				log.Warningf("rtmp pull has been removed srcurl=%s", srcUrl)
				rtmpClient.(*RtmpRelay).Stop()
			}
		}
	}
	self.mutexSrcUrls.RUnlock()
}

func (self *MultipleReley) pulllistStart() {
	self.pulllistEndFlag = false
	log.Warningf("pulllistStart starting:%s", self.dstUrl)
	for {
		if !self.IsStart() {
			break
		}

		startNumber := 0
		if !self.pulllistRunningFlag {
			break
		}
		self.mutexSrcUrls.RLock()
		log.Warningf("pulllistStart srcUrls:%v", self.srcUrls)
		for _, item := range self.srcUrls {
			if !self.pulllistRunningFlag {
				break
			}
			srcUrl := item.Srcurl
			keyurl, err := self.GetKeyString(srcUrl)
			if err != nil {
				continue
			}
			dstUrl := fmt.Sprintf("rtmp://127.0.0.1/%s", keyurl)
			log.Infof("local_push %s", dstUrl)

			headerString := srcUrl[0:4]
			if headerString == "http" {
				if self.flvRelays.Has(srcUrl) {
					httpRelay, _ := self.flvRelays.Get(srcUrl)
					if !httpRelay.(*FlvPull).IsStart() {
						log.Infof("flv pull continue to start %s", srcUrl)
						err := httpRelay.(*FlvPull).Start()
						if err == nil {
							startNumber++
						}
					} else {
						startNumber++
					}
				} else {
					httpRelay := NewFlvPull(&srcUrl, &dstUrl)
					self.flvRelays.Set(srcUrl, httpRelay)
					log.Infof("flv pull start %s", srcUrl)
					err := httpRelay.Start()
					if err == nil {
						startNumber++
					}
				}
			} else if headerString == "rtmp" {
				if self.rtmpRelays.Has(srcUrl) {
					rtmpClient, _ := self.rtmpRelays.Get(srcUrl)
					if !rtmpClient.(*RtmpRelay).IsStart() {
						log.Infof("rtmp pull continue to start %s", srcUrl)
						err := rtmpClient.(*RtmpRelay).Start()
						if err == nil {
							startNumber++
						}
					} else {
						startNumber++
					}
				} else {
					rtmpClient := NewRtmpRelay(&srcUrl, &dstUrl)
					self.rtmpRelays.Set(srcUrl, rtmpClient)
					log.Infof("rtmp pull start %s", srcUrl)
					err := rtmpClient.Start()
					if err == nil {
						startNumber++
					}
				}

			}
		}

		if startNumber >= len(self.srcUrls) {
			self.mutexSrcUrls.RUnlock()
			break
		}
		self.mutexSrcUrls.RUnlock()
	}
	self.pulllistEndFlag = true
	log.Warningf("pulllistStart done:%s", self.dstUrl)
}

func (self *MultipleReley) getIndexbySrcid(srcid int) int {
	retIndex := -1

	self.mutexSrcUrls.RLock()
	for index, item := range self.srcUrls {
		if item.Srcid == srcid {
			retIndex = index
			break
		}
	}
	self.mutexSrcUrls.RUnlock()

	return retIndex
}

func (self *MultipleReley) onSendPacket() {
	debugCount := 0
	var firstTimestamp int64
	var firstChunkTimestamp int64
	var nowTimestamp int64

	var firstPacketTimestamp int64
	var tryCount int64

	defer func() {
		self.mutexRemotepush.Lock()
		self.remotepush.Close(nil)
		self.remotepush = nil
		self.mutexRemotepush.Unlock()
	}()

	for {
		if !self.IsStart() {
			break
		}

		csPacket := self.getChunkStream()
		if csPacket == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		nowTimestamp = time.Now().UnixNano() / 1000 / 1000
		if firstPacketTimestamp == 0 {
			firstPacketTimestamp = nowTimestamp
		}
		if firstTimestamp == 0 {
			firstTimestamp = nowTimestamp
		}
		if firstChunkTimestamp == 0 || firstChunkTimestamp > int64(csPacket.Timestamp) {
			firstChunkTimestamp = int64(csPacket.Timestamp)
			firstTimestamp = nowTimestamp
		}

		diffTime := (int64(csPacket.Timestamp) - firstChunkTimestamp) - (nowTimestamp - firstTimestamp)
		if diffTime > 50 {
			diffInt := time.Duration(diffTime)
			time.Sleep(diffInt * time.Millisecond)
		}

		csPacket.Timestamp = uint32(nowTimestamp - firstPacketTimestamp)
		if debugCount%BUFFER_DEF_SIZE == 0 {
			index := self.getIndexbySrcid(self.playId)
			if index >= 0 {
				self.mutexSrcUrls.RLock()
				log.Infof("remote push length=%d, typeid=%d, list_size=%d, \r\npackt_timestamp=%d, now=%d, firstChunkTimestamp=%d, firsttime=%d, diffTime=%d\r\n%s-->%s",
					csPacket.Length, csPacket.TypeID, self.getListLen(), csPacket.Timestamp, nowTimestamp, firstChunkTimestamp, firstTimestamp, diffTime,
					self.srcUrls[index].Srcurl, self.dstUrl)
				self.mutexSrcUrls.RUnlock()
			}
		}
		debugCount++

		self.mutexRemotepush.Lock()
		err := self.remotepush.Write(*csPacket)
		self.mutexRemotepush.Unlock()

		if err != nil {
			tryCount++
			time.Sleep(50 * time.Millisecond)
		} else {
			tryCount = 0
		}
		if tryCount > 3 && self.IsStart() {
			self.mutexRemotepush.Lock()
			self.remotepush.Close(nil)
			self.remotepush = core.NewConnClient()

			err := self.remotepush.Start(self.dstUrl, "publish")
			if err != nil {
				self.remotepush = nil
				self.mutexRemotepush.Unlock()

				log.Error("remote push start error:", err)
				time.Sleep(50 * time.Millisecond)
			} else {
				self.mutexRemotepush.Unlock()
			}
		}
	}
}

func (self *MultipleReley) SetActiveSrcUrl(srcid int) error {
	index := self.getIndexbySrcid(srcid)
	if index < 0 {
		return errors.New(fmt.Sprintf("srcid(%d) not found", srcid))
	}

	if self.playId == srcid {
		self.mutexSrcUrls.RLock()
		outputStr := fmt.Sprintf("srcUrl has already been pulled, %s", self.srcUrls[index])
		self.mutexSrcUrls.RUnlock()
		log.Error(outputStr)
		return errors.New(outputStr)
	}

	log.Infof("SetActiveSrcUrl index=%d, self.playId=%d", index, self.playId)
	self.playId = srcid
	go self.pullStart(true)

	return nil
}

func (self *MultipleReley) getListLen() int {
	self.mutexList.RLock()
	listLen := self.chunkList.Len()
	self.mutexList.RUnlock()

	return listLen
}

func (self *MultipleReley) getChunkStream() *core.ChunkStream {
	self.mutexList.RLock()
	listLen := self.chunkList.Len()
	self.mutexList.RUnlock()

	if listLen == 0 {
		return nil
	}
	if (listLen < self.listBufferSize) && !self.listBufferInit {
		return nil
	}

	self.listBufferInit = true

	self.mutexList.Lock()
	rcItem := self.chunkList.Front()
	rcData := rcItem.Value.(*core.ChunkStream)
	self.chunkList.Remove(rcItem)
	self.mutexList.Unlock()

	return rcData
}

func (self *MultipleReley) insertChunkStream(rc *core.ChunkStream) {
	listlen := self.getListLen()
	if listlen >= self.listBufferMax {
		time.Sleep(50 * time.Millisecond)
	}

	self.mutexList.Lock()
	self.chunkList.PushBack(rc)
	self.mutexList.Unlock()
}

func (self *MultipleReley) rcvLocalPlay() {

	defer func() {
		self.SetLocalPlayStart(false)

		self.mutexLocalplay.RLock()
		if self.localplay != nil {
			self.mutexLocalplay.RUnlock()

			self.mutexLocalplay.Lock()
			self.localplay.Close(nil)
			self.mutexLocalplay.Unlock()
		} else {
			self.mutexLocalplay.RUnlock()
		}

		log.Warningf("rcvLocalPlay end")
		if e := recover(); e != nil {
			log.Errorf("rcvLocalPlay cs channel has already been closed:%v", e)
			return
		}
	}()
	tryCount := 0
	index := self.getIndexbySrcid(self.playId)
	if index < 0 {
		log.Errorf("srcid(%d) error", self.playId)
		return
	}
	self.mutexSrcUrls.RLock()
	keyUrl, _ := self.GetKeyString(self.srcUrls[index].Srcurl)
	self.mutexSrcUrls.RUnlock()

	srcUrl := fmt.Sprintf("rtmp://127.0.0.1/%s", keyUrl)
	log.Warningf("rcvLocalPlay start:%v", srcUrl)
	for {
		rc := &core.ChunkStream{}

		self.mutexLocalplay.Lock()
		err := self.localplay.Read(rc)
		self.mutexLocalplay.Unlock()

		if err != nil {
			tryCount++
		}
		if (err != nil && err == io.EOF) || tryCount > 3 {
			break
		}
		if err != nil {
			continue
		}
		tryCount = 0

		switch rc.TypeID {
		case 20, 17:
			r := bytes.NewReader(rc.Data)
			self.mutexLocalplay.Lock()
			vs, err := self.localplay.DecodeBatch(r, amf.AMF0)
			self.mutexLocalplay.Unlock()

			log.Infof("rcvPlayRtmpMediaPacket: vs=%v, err=%v", vs, err)
		case 18, 8, 9:
			if rc.TypeID == 18 {
				log.Infof("rcvPlayRtmpMediaPacket: metadata....")
			}
			self.insertChunkStream(rc)
		}
	}
}

func (self *MultipleReley) IsLocalPlayStart() bool {
	self.mutexIsLocalplayStart.RLock()
	ret := self.isLocalPlayStart
	self.mutexIsLocalplayStart.RUnlock()
	return ret
}

func (self *MultipleReley) SetLocalPlayStart(flag bool) {
	self.mutexIsLocalplayStart.Lock()
	self.isLocalPlayStart = flag
	self.mutexIsLocalplayStart.Unlock()
}

func (self *MultipleReley) pullStart(nowFlag bool) {
	var playconn *core.ConnClient

	if !nowFlag {
		time.Sleep(2 * time.Second)
	}

	lasttimestamp := time.Now().Unix()
	for {
		if !self.IsStart() {
			break
		}

		nowtimestamp := time.Now().Unix()
		if ((nowtimestamp - lasttimestamp) < 3) && !nowFlag {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		lasttimestamp = nowtimestamp
		playconn = core.NewConnClient()

		index := self.getIndexbySrcid(self.playId)
		if index < 0 {
			log.Errorf("srcid(%d) error", self.playId)
			break
		}

		self.mutexSrcUrls.RLock()
		keyUrl, _ := self.GetKeyString(self.srcUrls[index].Srcurl)
		self.mutexSrcUrls.RUnlock()

		srcUrl := fmt.Sprintf("rtmp://127.0.0.1/%s", keyUrl)
		err := playconn.Start(srcUrl, "play")
		if err != nil {
			playconn = nil
			continue
		}
		log.Warningf("localplay start nowflag=%v:%s ", nowFlag, srcUrl)
		break
	}
	startT := time.Now().UnixNano() / 1000 / 1000

	self.mutexLocalplay.RLock()
	if self.localplay != nil {
		self.mutexLocalplay.RUnlock()

		self.mutexLocalplay.Lock()
		self.localplay.Close(nil)
		self.mutexLocalplay.Unlock()

		for {
			if !self.IsLocalPlayStart() {
				break
			} else {
				time.Sleep(50 * time.Millisecond)
			}
		}
	} else {
		self.mutexLocalplay.RUnlock()
	}
	endT := time.Now().UnixNano() / 1000 / 1000
	costT := endT - startT

	log.Warningf("localplay close cost %d milliseconds", costT)

	self.mutexLocalplay.Lock()
	self.localplay = playconn
	self.mutexLocalplay.Unlock()

	self.SetLocalPlayStart(true)
	go self.rcvLocalPlay()
}

func (self *MultipleReley) IsStart() bool {
	self.mutexIsStart.RLock()
	ret := self.isStart
	self.mutexIsStart.RUnlock()
	return ret
}

func (self *MultipleReley) SetStartFlag(flag bool) {
	self.mutexIsStart.Lock()
	self.isStart = flag
	self.mutexIsStart.Unlock()
}

func (self *MultipleReley) Start() error {
	if self.IsStart() {
		return errors.New(fmt.Sprintf("MultipleReley has already started %s", self.dstUrl))
	}

	self.listBufferInit = false
	self.mutexSrcUrls.RLock()
	self.playId = self.srcUrls[0].Srcid
	self.mutexSrcUrls.RUnlock()

	self.mutexRemotepush.Lock()
	self.remotepush = core.NewConnClient()

	err := self.remotepush.Start(self.dstUrl, "publish")
	if err != nil {
		self.remotepush = nil
		self.mutexRemotepush.Unlock()
		log.Error("remote push start error:", err)
		return err
	} else {
		self.mutexRemotepush.Unlock()
	}
	log.Infof("multiple relay start dsturl=%s, listbuffersize=%d, listbuffermax=%d",
		self.dstUrl, self.listBufferSize, self.listBufferMax)
	self.SetStartFlag(true)

	self.mutexLocalplay.Lock()
	self.localplay = nil
	self.mutexLocalplay.Unlock()

	self.cs_chan = make(chan *core.ChunkStream, 5000)

	go self.onSendPacket()

	self.pulllistRunningFlag = true
	go self.pulllistStart()
	go self.pullStart(false)
	return nil
}

func (self *MultipleReley) Stop() {

	if !self.IsStart() {
		return
	}
	self.SetStartFlag(false)

	close(self.cs_chan)
	self.pulllistStop()
}
