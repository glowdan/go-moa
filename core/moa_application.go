package core

import (
	"fmt"
	"git.wemomo.com/bibi/go-moa/lb"
	"git.wemomo.com/bibi/go-moa/log4moa"
	"git.wemomo.com/bibi/go-moa/protocol"
	"git.wemomo.com/bibi/go-moa/proxy"
	log "github.com/blackbeans/log4go"
	"github.com/blackbeans/turbo"
	"github.com/blackbeans/turbo/client"
	"github.com/blackbeans/turbo/codec"
	"github.com/blackbeans/turbo/packet"
	"github.com/blackbeans/turbo/server"
)

type ServiceBundle func() []proxy.Service

type Application struct {
	remoting      *server.RemotingServer
	invokeHandler *proxy.InvocationHandler
	options       *MOAOption
	configCenter  *lb.ConfigCenter
}

func NewApplcation(configPath string, bundle ServiceBundle) *Application {
	services := bundle()

	options, err := LoadConfiruation(configPath)
	if nil != err {
		panic(err)
	}

	//修正serviceUri的后缀
	for i, s := range services {
		s.ServiceUri = (s.ServiceUri + options.serviceUriSuffix)
		services[i] = s
	}

	name := options.name + "/" + options.hostport
	rc := turbo.NewRemotingConfig(name,
		options.maxDispatcherSize,
		options.readBufferSize,
		options.readBufferSize,
		options.writeChannelSize,
		options.readChannelSize,
		options.idleDuration,
		50*10000)

	//需要开发对应的codec
	cf := func() codec.ICodec {
		return protocol.RedisGetCodec{32 * 1024}
	}

	//创建注册服务
	configCenter := lb.NewConfigCenter(options.registryType,
		options.registryHosts, options.hostport, services)

	app := &Application{}
	app.options = options
	app.configCenter = configCenter
	//moastat
	moaStat := log4moa.NewMoaStat(func() string {
		s := app.remoting.NetworkStat()
		return fmt.Sprintf("R:%dKB/%d\tW:%dKB/%d\tGo:%d\tCONN:%d", s.ReadBytes/1024,
			s.ReadCount,
			s.WriteBytes/1024, s.WriteCount, s.DispatcherGo, s.Connections)

	})

	app.invokeHandler = proxy.NewInvocationHandler(services, moaStat)

	//启动remoting
	remoting := server.NewRemotionServerWithCodec(options.hostport, rc, cf, app.packetDispatcher)
	app.remoting = remoting
	remoting.ListenAndServer()
	moaStat.StartLog()
	//注册服务
	configCenter.RegisteAllServices()
	log.InfoLog("moa-server", "Application|Start|SUCC|%s", name)
	return app
}

func (self Application) DestoryApplication() {

	//取消注册服务
	self.configCenter.Destroy()
	//关闭remoting
	self.remoting.Shutdown()
}

//需要开发对应的分包
func (self Application) packetDispatcher(remoteClient *client.RemotingClient, p *packet.Packet) {

	defer func() {
		if err := recover(); nil != err {
			log.ErrorLog("moa-server", "Application|packetDispatcher|FAIL|%s", err)
		}
	}()

	//这里面根据解析包的内容得到调用不同的service获得结果
	req, err := protocol.Wrap2MoaRequest(p.Data)
	if nil != err {
		log.ErrorLog("moa-server", "Application|packetDispatcher|Wrap2MoaRequest|FAIL|%s|%s", err, string(p.Data))
	} else {

		req.Timeout = self.options.processTimeout
		result := self.invokeHandler.Invoke(*req)
		resp, err := protocol.Wrap2ResponsePacket(p, result)
		if nil != err {
			log.ErrorLog("moa-server", "Application|packetDispatcher|Wrap2ResponsePacket|FAIL|%s|%s", err, result)
		} else {
			remoteClient.Write(*resp)
			//log.DebugLog("moa-server", "Application|packetDispatcher|SUCC|%s", *resp)
		}

	}
}
