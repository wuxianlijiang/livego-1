package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/livego/concurrent-map"
	"github.com/livego/configure"
	log "github.com/livego/logging"
	"github.com/livego/protocol/hls"
	"github.com/livego/protocol/httpflv"
	"github.com/livego/protocol/httpopera"
	"github.com/livego/protocol/rtmp"
	"github.com/livego/protocol/rtmp/rtmprelay"
)

var (
	version        = "v1.1"
	configfilename = flag.String("cfgfile", "livego.cfg", "live configure filename")
	loglevel       = flag.String("loglevel", "info", "log level")
	logfile        = flag.String("logfile", "livego.log", "log file path")
)

var StaticPulMgr *rtmprelay.StaticPullManager

func init() {
	flag.Parse()
	log.SetOutputByName(*logfile)
	log.SetRotateByDay()
	log.SetLevelByString(*loglevel)
}

func startStaticPull() {
	time.Sleep(time.Second * 5)
	var pullArray []configure.StaticPullInfo

	pullArray, bRet := configure.GetStaticPullList()

	log.Infof("startStaticPull: pullArray=%v, ret=%v", pullArray, bRet)
	if bRet && pullArray != nil && len(pullArray) > 0 {
		StaticPulMgr = rtmprelay.NewStaticPullManager(configure.GetListenPort(), pullArray)
		if StaticPulMgr != nil {
			StaticPulMgr.Start()
		}
	}

}

func stopStaticPull() {
	if StaticPulMgr != nil {
		StaticPulMgr.Stop()
	}
}

func startHls() (*hls.Server, net.Listener) {
	hlsaddr := fmt.Sprintf(":%d", configure.GetHlsPort())
	hlsListen, err := net.Listen("tcp", hlsaddr)
	if err != nil {
		log.Error(err)
	}

	hlsServer := hls.NewServer()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("HLS server panic: ", r)
			}
		}()
		log.Info("HLS listen On", hlsaddr)
		hlsServer.Serve(hlsListen)
	}()
	return hlsServer, hlsListen
}

func startRtmp(stream *rtmp.RtmpStream, hlsServer *hls.Server) {
	rtmpAddr := fmt.Sprintf(":%d", configure.GetListenPort())

	rtmpListen, err := net.Listen("tcp", rtmpAddr)
	if err != nil {
		log.Fatal(err)
	}

	var rtmpServer *rtmp.Server

	if hlsServer == nil {
		rtmpServer = rtmp.NewRtmpServer(stream, nil)
		log.Infof("hls server disable....")
	} else {
		rtmpServer = rtmp.NewRtmpServer(stream, hlsServer)
		log.Infof("hls server enable....")
	}

	defer func() {
		if r := recover(); r != nil {
			log.Error("RTMP server panic: ", r)
		}
	}()
	log.Info("RTMP Listen On", rtmpAddr)
	rtmpServer.Serve(rtmpListen)
}

func startHTTPFlv(stream *rtmp.RtmpStream, l net.Listener) net.Listener {
	var flvListen net.Listener
	var err error

	httpFlvAddr := fmt.Sprintf(":%d", configure.GetHttpFlvPort())
	if l == nil {
		log.Info("new flv listen...")
		flvListen, err = net.Listen("tcp", httpFlvAddr)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		flvListen = l
	}

	hdlServer := httpflv.NewServer(stream)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Fatal("HTTP-FLV server panic: ", r)
			}
		}()
		log.Info("HTTP-FLV listen On", httpFlvAddr)
		hdlServer.Serve(flvListen)
	}()
	return flvListen
}

func startHTTPOpera(stream *rtmp.RtmpStream, l net.Listener) net.Listener {
	var opListen net.Listener
	var err error

	operaAddr := fmt.Sprintf(":%d", configure.GetHttpOperPort())
	rtmpAddr := fmt.Sprintf(":%d", configure.GetListenPort())
	if l == nil {
		opListen, err = net.Listen("tcp", operaAddr)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		opListen = l
	}

	opServer := httpopera.NewServer(stream, rtmpAddr)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("HTTP-Operation server panic: ", r)
			}
		}()
		log.Info("HTTP-Operation listen On", operaAddr)
		opServer.Serve(opListen)
	}()

	return opListen
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("livego panic: ", r)
			time.Sleep(1 * time.Second)
		}
	}()
	log.Info("start_livego: ", version)
	log.Info("map hash count", cmap.SHARD_COUNT)
	err := configure.LoadConfig(*configfilename)
	if err != nil {
		return
	}

	var hlsServer *hls.Server
	//var hlsListener net.Listener
	//var flvListener net.Listener

	stream := rtmp.NewRtmpStream()

	go startStaticPull()
	defer stopStaticPull()

	if configure.IsHlsEnable() {
		hlsServer, _ = startHls()
		//log.Info("hls listen", hlsListener)
	}

	if configure.IsHttpFlvEnable() {
		if configure.GetHlsPort() == configure.GetHttpFlvPort() {
			log.Error("hls port", configure.GetHlsPort(), "and http flv port", configure.GetHttpFlvPort(), "conflict.")
			//flvListener = startHTTPFlv(stream, hlsListener)
			return
		} else {
			//log.Info("not equal", "hls port", configure.GetHlsPort(), "http flv port", configure.GetHttpFlvPort())
			startHTTPFlv(stream, nil)
		}
	}

	if configure.IsHttpOperEnable() {
		if configure.IsHlsEnable() && configure.GetHlsPort() == configure.GetHttpOperPort() {
			log.Error("hls port", configure.GetHlsPort(), "http oper port", configure.GetHttpOperPort(), "conflict.")
			//startHTTPOpera(stream, hlsListener)
			return
		} else if configure.IsHttpFlvEnable() && configure.GetHttpFlvPort() == configure.GetHttpOperPort() {
			log.Info("http flv", configure.GetHttpFlvPort(), "http oper port", configure.GetHttpOperPort(), "conflict")
			//startHTTPOpera(stream, flvListener)
		} else {
			//log.Info("hls port", configure.GetHlsPort(), "http flv", configure.GetHttpFlvPort(),
			//	"http oper port", configure.GetHttpOperPort())
			startHTTPOpera(stream, nil)
		}
	}

	if configure.IsHlsEnable() {
		startRtmp(stream, hlsServer)
	} else {
		startRtmp(stream, nil)
	}
}
