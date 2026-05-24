//go:build windows
// +build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"github.com/rs/cors"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/disk"
	"github.com/shirou/gopsutil/mem"
	"github.com/shirou/gopsutil/process"
)

var (
	Version        = "0.0.0"
	CommitHash     = "n/a"
	BuildTimestamp = "n/a"
)

const AUTHKEY_HASH_REG = "WinBoatAuthHash"
const AUTHKEY_HASH_OEM_LOCATION = "C:\\OEM\\auth.hash"

type Metrics struct {
	CPU struct {
		Usage     float64 `json:"usage"`     // Percentage, 0-100
		Frequency uint64  `json:"frequency"` // MHz
	} `json:"cpu"`
	RAM struct {
		Used       uint64  `json:"used"`       // MB
		Total      uint64  `json:"total"`      // MB
		Percentage float64 `json:"percentage"` // %
	} `json:"ram"`
	Disk struct {
		Used       uint64  `json:"used"`       // MB
		Total      uint64  `json:"total"`      // MB
		Percentage float64 `json:"percentage"` // %
	} `json:"disk"`
}

type RDPStatusResponse struct {
	RdpConnection bool `json:"rdpConnected"`
}

func getApps(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-Command", "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; & '.\\scripts\\apps.ps1'")
	output, err := cmd.Output()
	if err != nil {
		http.Error(w, "Failed to execute script: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(output)
}

func getHealth(w http.ResponseWriter, r *http.Request) {
	// Simple health check
	response := map[string]string{"status": "ok"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func getVersion(w http.ResponseWriter, r *http.Request) {
	response := map[string]string{
		"version":     Version,
		"commit_hash": CommitHash,
		"build_time":  BuildTimestamp,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func getMetrics(w http.ResponseWriter, r *http.Request) {
	// CPU stats
	cpuPercent, err := cpu.Percent(time.Second/4, false)
	if err != nil {
		http.Error(w, "Failed to get CPU stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	cpuInfo, err := cpu.Info()
	if err != nil || len(cpuInfo) == 0 {
		http.Error(w, "Failed to get CPU info: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// RAM stats
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		http.Error(w, "Failed to get RAM stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Disk stats (using first disk, adjust path if needed)
	diskInfo, err := disk.Usage("C:\\")
	if err != nil {
		http.Error(w, "Failed to get disk stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build metrics struct
	metrics := Metrics{}
	metrics.CPU.Usage = cpuPercent[0]              // Total CPU usage
	metrics.CPU.Frequency = uint64(cpuInfo[0].Mhz) // First CPU's frequency
	metrics.RAM.Used = memInfo.Used / 1024 / 1024  // Bytes to MB
	metrics.RAM.Total = memInfo.Total / 1024 / 1024
	metrics.RAM.Percentage = float64(metrics.RAM.Used) / float64(metrics.RAM.Total) * 100
	metrics.Disk.Used = diskInfo.Used / 1024 / 1024
	metrics.Disk.Total = diskInfo.Total / 1024 / 1024
	metrics.Disk.Percentage = diskInfo.UsedPercent

	// Send it
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(metrics)
}

func getBalloonStatus(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("C:\\Windows\\Drivers\\Balloon\\blnsvr.exe", "status")
	output, err := cmd.CombinedOutput()

	response := map[string]string{}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1060:
				response["status"] = "not-installed"
				response["details"] = string(output)
			case 1, 1062:
				response["status"] = "stopped"
				response["details"] = string(output)
			default:
				response["status"] = "error"
				response["details"] = string(output)
			}
		} else {
			response["status"] = "error"
			response["details"] = err.Error()
		}
	} else {
		response["status"] = "running"
		response["details"] = string(output)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func installBalloon(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("C:\\Windows\\Drivers\\Balloon\\blnsvr.exe", "-i")
	output, err := cmd.CombinedOutput()

	response := map[string]string{}

	if err != nil {
		response["result"] = "error"
		response["details"] = string(output)
	} else {
		response["result"] = "success"
		response["details"] = string(output)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func getRdpConnectedStatus(w http.ResponseWriter, r *http.Request) {
	// Check for RDP Status via quser.exe
	// But use Powershell for locale-independent processing
	cmd := exec.Command("powershell", "-NoProfile", "-NoLogo", "-Command", "if(C:\\Windows\\System32\\quser.exe 2>&1 | Select-Object -Skip 1 | ForEach-Object { $_ -replace '\\s{2,}', ',' } | ConvertFrom-Csv -Header 'UserName','SessionName','ID','State','IdleTime','LogonTime' | Where-Object { $_.SessionName -match 'rdp'} | Where-Object { $_.State.startsWith('A') }) {'1'} else {'0'}")
	output, _ := cmd.Output()

	response := RDPStatusResponse{
		RdpConnection: strings.TrimSpace(string(output)) == "1",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

type ProcessStatusResponse struct {
	Running bool `json:"running"`
	RealPath string `json:"realPath"`
}

// ExpandWindowsEnv replaces %VAR% style environment variables.
// Example: "%WINDIR%\\System32" → "C:\\Windows\\System32"
func ExpandWindowsEnv(s string) string {
	re := regexp.MustCompile(`%([^%]+)%`)

	return re.ReplaceAllStringFunc(s, func(match string) string {
		// Extract variable name (remove the % signs)
		varName := match[1 : len(match)-1]

		// Look up the variable (case-insensitive on Windows)
		if value, ok := os.LookupEnv(varName); ok {
			return value
		}

		// Keep original if variable is not found
		return match
	})
}

// ResolveShortcut returns the target path of a .lnk file
func ResolveShortcut(lnkPath string) (string, error) {
	// Initialize COM
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return "", fmt.Errorf("failed to initialize COM: %w", err)
	}
	defer ole.CoUninitialize()

	// Create WScript.Shell object
	wshell, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return "", fmt.Errorf("failed to create WScript.Shell: %w", err)
	}
	defer wshell.Release()

	wshellDisp, err := wshell.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return "", fmt.Errorf("failed to query interface: %w", err)
	}
	defer wshellDisp.Release()

	// Create shortcut object
	shortcutDisp, err := oleutil.CallMethod(wshellDisp, "CreateShortcut", lnkPath)
	if err != nil {
		return "", fmt.Errorf("failed to create shortcut: %w", err)
	}
	shortcut := shortcutDisp.ToIDispatch()
	defer shortcut.Release()

	// Get TargetPath
	target, err := oleutil.GetProperty(shortcut, "TargetPath")
	if err != nil {
		return "", fmt.Errorf("failed to get TargetPath: %w", err)
	}

	return target.ToString(), nil
}

func IsProcessRunningByPath(fullPath string) bool {
	procs, err := process.Processes()
	if err != nil {
		return false
	}

	for _, p := range procs {
		exe, err := p.Exe()
		if err == nil && strings.EqualFold(exe, fullPath) {
			return true
		}
	}

	return false
}

func getProcessStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path query param required", http.StatusBadRequest)
		return
	}

	envCmdPath := ExpandWindowsEnv(path)

	var cmdIsRunning bool
	var realPath string

	// If the path has a lnk suffix, resolve it to the actual path first
	if strings.HasSuffix(envCmdPath, ".lnk") {
		exePath, err := ResolveShortcut(envCmdPath)
		if err == nil {
			realPath = ExpandWindowsEnv(exePath)
		} else {
			realPath = envCmdPath
		}
	} else {
		realPath = envCmdPath
	}

	cmdIsRunning = IsProcessRunningByPath(realPath)

	response := ProcessStatusResponse{
		Running: cmdIsRunning,
		RealPath: realPath,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func applyUpdate(w http.ResponseWriter, r *http.Request) {
	// Verify password
	expectedHash, err := getSecureRegKey(AUTHKEY_HASH_REG)
	if err != nil || expectedHash == nil {
		http.Error(w, "Unauthorized: failed to read auth hash", http.StatusUnauthorized)
		return
	}

	password := r.FormValue("password")
	if password == "" {
		http.Error(w, "Unauthorized: password is required", http.StatusUnauthorized)
		return
	}

	isValid, err := verifyPasswordSecure(*expectedHash, password)
	if err != nil || !isValid {
		http.Error(w, "Unauthorized: invalid password", http.StatusUnauthorized)
		return
	}

	r.ParseMultipartForm(100 << 20) // Limit upload size to 100MB
	var buf bytes.Buffer

	// Grab the file
	file, _, err := r.FormFile("updateFile")
	if err != nil {
		http.Error(w, "Failed to get update file: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Copy to buffer & close the file
	io.Copy(&buf, file)
	file.Close()

	// Check zip magic bytes
	if !(buf.Bytes()[0] == 'P' && buf.Bytes()[1] == 'K' && buf.Bytes()[2] == 3 && buf.Bytes()[3] == 4) {
		http.Error(w, "Uploaded file is not a valid ZIP archive", http.StatusBadRequest)
		return
	}

	// Make temp directory
	tmpDir, err := os.MkdirTemp("", "winboat-update")
	if err != nil {
		http.Error(w, "Failed to create temp directory: "+err.Error(), http.StatusInternalServerError)
	}

	// Write to temp file
	fileName := "update.zip"
	tmpFilePath := filepath.Join(tmpDir, fileName)
	err = os.WriteFile(tmpFilePath, buf.Bytes(), 0644)

	if err != nil {
		http.Error(w, "Failed to write temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Get current executable path & directory
	exePath, err := os.Executable()
	if err != nil {
		http.Error(w, "Failed to get executable path: "+err.Error(), http.StatusInternalServerError)
		return
	}
	appDir := filepath.Dir(exePath)
	origScriptPath := filepath.Join(appDir, "scripts\\update.ps1")
	scriptPath := filepath.Join(tmpDir, "update.ps1")

	// Copy the update script to the temp directory
	copyFile(origScriptPath, scriptPath)

	// Return success & the paths
	response := map[string]string{
		"status":    "updating",
		"temp_path": tmpFilePath,
		"filename":  fileName,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	// Create & run the script
	cmd := exec.Command("cmd", "/C", "start", "/B", "powershell",
		"-ExecutionPolicy", "Bypass", "-File", scriptPath,
		"-AppPath", appDir,
		"-UpdateFilePath", tmpFilePath,
		"-ServiceName", "WinBoatGuestServer")

	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	// The script will take care of the rest, including stopping this service
	cmd.Start()

}

func getIcon(w http.ResponseWriter, r *http.Request) {
	path := r.PostFormValue("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	cmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-File", "scripts\\get-icon.ps1", "-path", path)
	output, err := cmd.Output()
	if err != nil {
		http.Error(w, "Failed to execute script: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write(output)
}

func setAuthHash(w http.ResponseWriter, r *http.Request) {
	// If there's an existing hash, reject
	existingHash, err := getSecureRegKey(AUTHKEY_HASH_REG)
	if err != nil {
		http.Error(w, "Failed to read existing auth hash: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if existingHash != nil {
		http.Error(w, "Auth hash already set", http.StatusBadRequest)
		return
	}

	// Get auth hash from POST body
	authHash := r.PostFormValue("authHash")
	if authHash == "" {
		http.Error(w, "authHash is required", http.StatusBadRequest)
		return
	}

	// Store it securely
	err = setSecureRegKey(AUTHKEY_HASH_REG, authHash)
	if err != nil {
		http.Error(w, "Failed to store auth hash: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := map[string]string{"status": "ok"}
	json.NewEncoder(w).Encode(response)
}

func getWindows(w http.ResponseWriter, r *http.Request) {
	// Execute PowerShell script to enumerate RAIL windows
	cmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-File", "scripts\\get-windows.ps1")
	output, err := cmd.Output()
	if err != nil {
		http.Error(w, "Failed to execute script: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(output)
}

func main() {
	// Try to initialize auth key hash if not present
	existingHash, err := getSecureRegKey(AUTHKEY_HASH_REG)
	if err != nil {
		log.Println("Warning: failed to read auth hash from registry:", err)
	}

	// Hash not found in registry
	if existingHash == nil {
		// Check OEM location and try to read from there
		oemHashBytes, err := os.ReadFile(AUTHKEY_HASH_OEM_LOCATION)
		if err != nil {
			log.Println("Auth hash not found in registry or OEM location.")
		} else {
			oemHash := string(bytes.TrimSpace(oemHashBytes))
			err = setSecureRegKey(AUTHKEY_HASH_REG, oemHash)
			if err != nil {
				log.Println("Failed to initialize auth hash from OEM location:", err)
			} else {
				log.Println("Initialized auth hash from OEM location.")
			}
		}
	}

	// Setup routes
	r := mux.NewRouter()
	r.HandleFunc("/apps", getApps).Methods("GET")
	r.HandleFunc("/health", getHealth).Methods("GET")
	r.HandleFunc("/version", getVersion).Methods("GET")
	r.HandleFunc("/metrics", getMetrics).Methods("GET")
	r.HandleFunc("/balloon/status", getBalloonStatus).Methods("GET")
	r.HandleFunc("/balloon/install", installBalloon).Methods("POST")
	r.HandleFunc("/rdp/status", getRdpConnectedStatus).Methods("GET")
	r.HandleFunc("/process/status", getProcessStatus).Methods("GET")
	r.HandleFunc("/update", applyUpdate).Methods("POST")
	r.HandleFunc("/get-icon", getIcon).Methods("POST")
	r.HandleFunc("/auth/set-hash", setAuthHash).Methods("POST")
	r.HandleFunc("/windows", getWindows).Methods("GET")
	handler := cors.Default().Handler(r)

	log.Println("Starting WinBoat Guest Server on :7148...")
	if err := http.ListenAndServe(":7148", handler); err != nil {
		log.Fatal("Server failed: ", err)
	}
}
