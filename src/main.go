package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/kardianos/service"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/mem"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var logger service.Logger

type program struct {
	exit chan struct{}
}

type Block struct {
	Try     func()
	Catch   func(Exception)
	Finally func()
}

type Exception interface{}

func Throw(up Exception) {
	panic(up)
}

func (tcf Block) Do() {
	if tcf.Finally != nil {
		defer tcf.Finally()
	}

	if tcf.Catch != nil {
		defer func() {
			if r := recover(); r != nil {
				tcf.Catch(r)
			}
		}()
	}
	tcf.Try()

}

func (p *program) Start(s service.Service) error {
	if service.Interactive() {
		logger.Info("Running in terminal.")
	} else {
		logger.Info("Running under service manager.")
	}
	p.exit = make(chan struct{})
	go p.run()
	return nil
}

func (p *program) run() error {
	logger.Infof("Api_metricas running %v.", service.Platform())
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case tm := <-ticker.C:
			logger.Infof("Still running GetMetrics() at %v...", tm)
			postmain()
		case <-p.exit:
			ticker.Stop()
			return nil
		}
	}
}

func (p *program) Stop(s service.Service) error {
	logger.Info("Api_metricas Stopping!")
	close(p.exit)
	return nil
}

func main() {
	svcFlag := flag.String("service", "", "Control the system service.")
	flag.Parse()

	options := make(service.KeyValue)
	options["Restart"] = "on-success"
	options["SuccessExitStatus"] = "1 2 8 SIGKILL"
	svcConfig := &service.Config{
		Name:        "Api_metricas",
		DisplayName: "Go Service Example for Logging",
		Description: "This is an example Go service that outputs log messages.",
		Dependencies: []string{
			"Requires=network.target",
			"After=network-online.target syslog.target"},
		Option: options,
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	check(err)

	errs := make(chan error, 5)
	logger, err = s.Logger(errs)
	check(err)

	go func() {
		for {
			err := <-errs
			check(err)
		}
	}()

	if len(*svcFlag) != 0 {
		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}
	err = s.Run()
	if err != nil {
		logger.Error(err)
	}
}

func postmain() {

	_hostMetrics := getMetrics()

	Block{
		Try: func() {
			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)

			client, err := mongo.Connect(ctx, options.Client().ApplyURI(
				"mongodb://root:"+url.QueryEscape("@H1lcotadmin")+"@192.168.2.2:27017"))
			check(err)

			collection := client.Database("HTERRACOTA").Collection("info_pc")

			filter := bson.D{{"hostiduiid", strings.Replace(_hostMetrics.HostIDUiid, "\"", "", 2)}}

			var result hostMetric

			err = collection.FindOne(context.TODO(), filter).Decode(&result)
			if err != nil {
				_, err := collection.InsertOne(context.TODO(), _hostMetrics)
				check(err)
				return
			}

			update := bson.M{"$set": _hostMetrics}

			collection.UpdateOne(
				context.Background(),
				filter,
				update,
			)

			err = client.Disconnect(context.TODO())
		},
		Catch: func(e Exception) {
			log.Printf("ERROR Connect")

		},
		Finally: func() {
			log.Printf("RETURN")
		},
	}.Do()

	return

}

func getMetrics() *hostMetric {

	_hostMetrics := new(hostMetric)

	t := time.Now()

	_hostMetrics.FechaUpdate = t.String()

	runtimeOS := runtime.GOOS

	vmStat, err := mem.VirtualMemory()
	check(err)

	diskStat, err := disk.Usage("/")
	check(err)

	cpuStat, err := cpu.Info()
	check(err)

	percentage, err := cpu.Percent(0, true)
	check(err)

	hostStat, err := host.Info()
	check(err)

	interfStat, err := net.Interfaces()
	check(err)

	_hostMetrics.Os = runtimeOS
	_hostMetrics.TotalMemory = strconv.FormatUint(vmStat.Total, 10)
	_hostMetrics.FreeMemory = strconv.FormatUint(vmStat.Free, 10)
	_hostMetrics.PercentageUsedMemory = strconv.FormatFloat(vmStat.UsedPercent, 'f', 2, 64)
	_hostMetrics.TotalDiskSpace = strconv.FormatUint(diskStat.Total, 10)
	_hostMetrics.UsedDiskSpace = strconv.FormatUint(diskStat.Used, 10)
	_hostMetrics.FreeDiskDpace = strconv.FormatUint(diskStat.Free, 10)
	_hostMetrics.PercentageDiskSpaceUsage = strconv.FormatFloat(diskStat.UsedPercent, 'f', 2, 64)
	_hostMetrics.CPUCores = strconv.FormatInt(int64(cpuStat[0].Cores), 10)
	_hostMetrics.Hostname = hostStat.Hostname
	_hostMetrics.Uptime = strconv.FormatUint(hostStat.Uptime, 10)
	_hostMetrics.NumbersOfProssesRunning = strconv.FormatUint(hostStat.Procs, 10)
	_hostMetrics.Platform = hostStat.Platform
	_hostMetrics.HostIDUiid = hostStat.HostID

	for _, cpupercent := range percentage {
		_x := cpuNode{}
		_x.CPUIndexNumber = strconv.FormatInt(int64(cpuStat[0].CPU), 10)
		_x.VendorID = cpuStat[0].VendorID
		_x.Family = cpuStat[0].Family
		_x.ModelName = cpuStat[0].ModelName
		_x.Speed = strconv.FormatFloat(cpuStat[0].Mhz, 'f', 2, 64)
		_x.CPUUsedPercentage = strconv.FormatFloat(cpupercent, 'f', 2, 64)
		_hostMetrics.Cores = append(_hostMetrics.Cores, _x)
	}

	for _, interf := range interfStat {
		_iterface := iterface{}
		_iterface.InterfaceName = interf.Name
		_iterface.HardwareMacAddress = interf.HardwareAddr.String()

		for _, flag := range strings.Split(interf.Flags.String(), "|") {
			_iterface.Flags = append(_iterface.Flags, flag)
		}

		addrs, _ := interf.Addrs()

		for _, addr := range addrs {
			_iterface.Ips = append(_iterface.Ips, addr.String())
		}

		_hostMetrics.Interfaces = append(_hostMetrics.Interfaces, _iterface)
	}

	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist.exe", "/v", "/FO", "csv")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		check(err)
		outStr := string(stdout.Bytes())
		whriteInFile("prosses", outStr)

		lines, err := readCsv("prosses.txt")
		check(err)

		frist := false
		for _, line := range lines {
			if frist {
				_prossesInfo := prossesInfo{}
				_prossesInfo.Nombredeimagen = line[0]
				_prossesInfo.PID = line[1]
				_prossesInfo.Nombredesesin = line[2]
				_prossesInfo.Nmdesesin = line[3]
				_prossesInfo.Usodememoria = line[4]
				_prossesInfo.Estado = line[5]
				_prossesInfo.Nombredeusuario = line[6]
				_prossesInfo.TiempodeCPU = line[7]
				_prossesInfo.Ttulodeventana = line[8]
				_hostMetrics.InfoProsses = append(_hostMetrics.InfoProsses, _prossesInfo)
			} else {
				frist = true
			}
		}

	} else {
		cmd := exec.Command("top", "-l", "1")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		check(err)
		outStr := string(stdout.Bytes())
		whriteInFile("prosses", outStr)
	}

	return _hostMetrics
}

func readInFile(filename string) string {
	dat, err := ioutil.ReadFile(getFilePath(filename))
	check(err)
	return string(dat)
}

func whriteInFile(filename string, dataToWhrite string) bool {
	err := ioutil.WriteFile(getFilePath(filename), []byte(dataToWhrite), 0644)
	check(err)
	return true
}

func readCsv(filename string) ([][]string, error) {
	f, err := os.Open(getPath() + filename)
	if err != nil {
		return [][]string{}, err
	}
	defer f.Close()
	lines, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return [][]string{}, err
	}
	return lines, nil
}

func getPath() string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	check(err)
	return dir + "/"
}

func getFilePath(filename string) string {
	filename = getPath() + filename + ".txt"
	return string(filename)
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

type hostMetric struct {
	FechaUpdate              string
	Status                   string
	Os                       string
	TotalMemory              string
	FreeMemory               string
	PercentageUsedMemory     string
	TotalDiskSpace           string
	UsedDiskSpace            string
	FreeDiskDpace            string
	PercentageDiskSpaceUsage string
	CPUCores                 string
	Hostname                 string
	Uptime                   string
	NumbersOfProssesRunning  string
	Platform                 string
	HostIDUiid               string
	Cores                    []cpuNode
	Interfaces               []iterface
	InfoProsses              []prossesInfo
}

type cpuNode struct {
	CPUIndexNumber    string
	VendorID          string
	Family            string
	ModelName         string
	Speed             string
	CPUUsedPercentage string
}

type iterface struct {
	InterfaceName      string
	HardwareMacAddress string
	Flags              []string
	Ips                []string
}

type prossesInfo struct {
	Nombredeimagen  string
	PID             string
	Nombredesesin   string
	Nmdesesin       string
	Usodememoria    string
	Estado          string
	Nombredeusuario string
	TiempodeCPU     string
	Ttulodeventana  string
}
