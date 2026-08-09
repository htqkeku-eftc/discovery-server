package main

import (
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cloudlink-delta/discovery-server/server"
	"github.com/cloudlink-delta/duplex"
	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func parsePredisposedInstances(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err == nil && len(list) > 0 {
		var result []string
		for _, s := range list {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}

	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		if trimmed := strings.Trim(strings.TrimSpace(p), "'\"[]"); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseICEServers(raw string) []webrtc.ICEServer {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var servers []webrtc.ICEServer
	if err := json.Unmarshal([]byte(raw), &servers); err == nil && len(servers) > 0 {
		return servers
	}

	var urlList []string
	if err := json.Unmarshal([]byte(raw), &urlList); err == nil && len(urlList) > 0 {
		var urls []string
		for _, u := range urlList {
			if trimmed := strings.TrimSpace(u); trimmed != "" {
				urls = append(urls, trimmed)
			}
		}
		if len(urls) > 0 {
			return []webrtc.ICEServer{{URLs: urls}}
		}
	}

	parts := strings.Split(raw, ",")
	var urls []string
	for _, p := range parts {
		if trimmed := strings.Trim(strings.TrimSpace(p), "'\"[]"); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	if len(urls) > 0 {
		return []webrtc.ICEServer{{URLs: urls}}
	}
	return nil
}

func main() {

	// CLI flags
	pflag.Int("log-level", (int)(zerolog.InfoLevel), "Logging level to use. Acceptable values range from -1 to 7. (default: 1 \"Info\")")
	pflag.String("config", "", "Path to JSON configuration file, i.e. ~/config.json")
	pflag.String("designation", "", "Globally unique designation (required)")

	// Duplex lib flags
	pflag.Bool("enable-pinger", false, "Enable ping/pong keepalive")
	pflag.Int64("ping-interval", 5000, "Ping/pong interval (in milliseconds)")
	pflag.Bool("session-secure", true, "Enable secure session server connections (required if session-hostname is set)")
	pflag.Int("session-port", 443, "Port where the session server is listening (required if session-hostname is set)")
	pflag.String("session-hostname", "peerjs.mikedev101.cc", "Hostname where the session server is listening")
	pflag.String("ice-servers", "", "Comma-separated list or JSON-encoded array of ICE server URLs")
	pflag.String("predisposed-instances", "", "Comma-separated list or JSON-encoded array of instances to connect to on startup")
	pflag.String("address", "127.0.0.1:3001", "Discovery server listener address")

	// Parse command-line flags
	pflag.Usage = func() {
		log.Println("Usage: discovery-server [options]")
		log.Println("Options:")
		pflag.PrintDefaults()
	}
	pflag.Parse()

	// Bind flags to viper
	viper.BindPFlag("log_level", pflag.Lookup("log-level"))
	viper.BindPFlag("config", pflag.Lookup("config"))
	viper.BindPFlag("designation", pflag.Lookup("designation"))
	viper.BindPFlag("enable_pinger", pflag.Lookup("enable-pinger"))
	viper.BindPFlag("ping_interval", pflag.Lookup("ping-interval"))
	viper.BindPFlag("session_secure", pflag.Lookup("session-secure"))
	viper.BindPFlag("session_port", pflag.Lookup("session-port"))
	viper.BindPFlag("session_hostname", pflag.Lookup("session-hostname"))
	viper.BindPFlag("ice_servers_flag", pflag.Lookup("ice-servers"))
	viper.BindPFlag("predisposed_instances_flag", pflag.Lookup("predisposed-instances"))
	viper.BindPFlag("address", pflag.Lookup("address"))

	// Load values from environment variables
	viper.AutomaticEnv()

	// Load config from file if provided
	if cfgFile := viper.GetString("config"); cfgFile != "" {
		viper.SetConfigFile(cfgFile)
		if err := viper.ReadInConfig(); err != nil {
			log.Fatalf("Failed to read config file: %v", err)
		}
	}

	logging_level := zerolog.Level(viper.GetInt("log_level"))
	serverCfg := server.Config{
		Designation: viper.GetString("designation"),
		Address:     viper.GetString("address"),
		Log_Level:   logging_level,
	}

	duplexCfg := duplex.Config{
		LogLevel:     logging_level,
		PingInterval: viper.GetInt64("ping_interval"),
		EnablePinger: viper.GetBool("enable_pinger"),
		Secure:       viper.GetBool("session_secure"),
		Port:         viper.GetInt("session_port"),
	}

	sessionHostnameProvided := pflag.CommandLine.Changed("session-hostname") || viper.IsSet("session_hostname")
	sessionSecureProvided := pflag.CommandLine.Changed("session-secure") || viper.IsSet("session_secure")
	sessionPortProvided := pflag.CommandLine.Changed("session-port") || viper.IsSet("session_port")

	if sessionHostnameProvided {
		duplexCfg.Hostname = viper.GetString("session_hostname")
	}

	var iceServers []webrtc.ICEServer
	if iceFlag := viper.GetString("ice_servers_flag"); iceFlag != "" {
		iceServers = parseICEServers(iceFlag)
	} else if viper.IsSet("ice_servers") {
		data, _ := json.Marshal(viper.Get("ice_servers"))
		iceServers = parseICEServers(string(data))
	}
	duplexCfg.ICEServers = iceServers

	// Verify loaded configuration
	if serverCfg.Designation == "" {
		log.Fatal("A designation is required. Please provide it via -designation or in a config.json file. See --help for more information.")
	}
	if sessionHostnameProvided {
		if !sessionSecureProvided || !sessionPortProvided {
			log.Fatal("When session-hostname is provided, both session-secure and session-port must also be provided explicitly. See --help for more information.")
		}
	}

	// Initialize the discovery server
	instance := server.New(&serverCfg, &duplexCfg)

	// Load predisposed instances if provided
	var predisposedInstances []string
	if predisposedFlag := viper.GetString("predisposed_instances_flag"); predisposedFlag != "" {
		predisposedInstances = parsePredisposedInstances(predisposedFlag)
	} else if viper.IsSet("predisposed_instances") {
		data, _ := json.Marshal(viper.Get("predisposed_instances"))
		predisposedInstances = parsePredisposedInstances(string(data))
	}
	if len(predisposedInstances) > 0 {
		instance.Predisposed_Instances = predisposedInstances
	}

	// Graceful shutdown handler
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		instance.Close <- true
		<-instance.Done
		os.Exit(1)
	}()

	// Run the server
	instance.Run()
}
