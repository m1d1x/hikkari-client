// Package cleanliness scores how "clean" the active egress looks to the
// outside internet and — crucially — explains WHY it might get fraud-flagged
// by Cloudflare/Turnstile, with concrete, actionable reasons.
//
// Everything here is free and keyless. The heavy lifting is ipapi.is, which
// reliably tags datacenter/hosting ranges, abuse-feed membership, and the
// ASN's registration country — the exact signals Cloudflare's edge keys off.
// ip-api adds geo/ISP niceties; IPQS is an OPTIONAL bonus that only fills in
// a precise numeric fraud-score if a key happens to be present.
//
// Fidelity note: when the tunnel is up, sing-box's TUN captures every dial
// this process makes, so the plain HTTP GETs below egress through the active
// profile exactly like the user's own traffic — same binary, same egress,
// same wire. So this is what the internet actually sees, not a guess.
//
// Mirrors core/health's caching shape: a background loop does the slow I/O;
// Report() serves the last snapshot instantly so FFI never blocks a UI thread.
package cleanliness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RefreshInterval — reputation changes slowly and the free sources are
// rate-limited, so we probe unhurriedly. A profile switch flips the exit IP;
// the next tick picks it up within this window.
const RefreshInterval = 20 * time.Second

// Reason is one concrete explanation of why the egress looks dirty (or a
// note about why it looks clean). Severity drives the colour in the UI.
type Reason struct {
	Severity string `json:"severity"` // "high" | "med" | "low" | "good"
	Title    string `json:"title"`    // what the signal is
	Fix      string `json:"fix"`      // how to improve it ("" if none)
}

// Report is the canonical shape the UI consumes. Field names are the JSON
// contract — keep them in sync with the Dart ServerCleanliness model.
type Report struct {
	Running    bool   `json:"running"`    // tunnel is up (else nothing to score)
	Checked    bool   `json:"checked"`    // we have a successful ip-api probe
	IP         string `json:"ip"`         // exit IP as the internet sees it
	ASN        string `json:"asn"`        // e.g. "AS210644"
	ASName     string `json:"asname"`     // e.g. "AEZA-AS"
	ASNType    string `json:"asnType"`    // "hosting" | "isp" | "business" | ...
	ASNCountry string `json:"asnCountry"` // ISO code where the ASN is registered
	ISP        string `json:"isp"`        //
	Org        string `json:"org"`        //
	Country    string `json:"country"`    // ISO code of the IP's geo
	City       string `json:"city"`       //

	IsHosting bool `json:"isHosting"` // datacenter/hosting range
	IsMobile  bool `json:"isMobile"`  // mobile carrier → usually very "clean"
	IsProxy   bool `json:"isProxy"`   // flagged proxy by any source
	IsVPN     bool `json:"isVpn"`     // flagged VPN
	IsTor     bool `json:"isTor"`     // Tor exit
	IsAbuser  bool `json:"isAbuser"`  // seen in abuse/spam feeds
	IsCrawler bool `json:"isCrawler"` // known crawler/bot range

	AbuserLabel string `json:"abuserLabel"` // "High"/"Elevated"/... from ipapi.is
	FraudScore  int    `json:"fraudScore"`  // IPQS 0-100, or -1 when no key
	AbuseEmail  string `json:"abuseEmail"`  // network abuse contact (from RIR)

	CFColo string `json:"cfColo"` // Cloudflare edge datacenter (e.g. "FRA")
	CFWarp string `json:"cfWarp"` // Cloudflare warp state

	Score     int      `json:"score"`     // 0-100 whiteness index (higher = cleaner)
	Verdict   string   `json:"verdict"`   // off|checking|clean|suspicious|datacenter|flagged
	Message   string   `json:"message"`   // short human summary for the collapsed pill
	Reasons   []Reason `json:"reasons"`   // WHY it looks the way it does
	CheckedAt string   `json:"checkedAt"` // RFC3339 of last successful probe
}

// Logger is the minimal logging surface we need.
type Logger interface {
	Logf(format string, args ...interface{})
}

// Checker holds the background state. Construct with New, start Run in a
// goroutine, read via Report.
type Checker struct {
	log     Logger
	running func() bool // true when the tunnel is up (from health)
	ipqsKey string      // optional; empty → fraud-score step skipped

	mu     sync.Mutex
	cached Report
}

// New wires the checker. `running` gates probing; `ipqsKey` is optional.
func New(log Logger, running func() bool, ipqsKey string) *Checker {
	return &Checker{
		log:     log,
		running: running,
		ipqsKey: strings.TrimSpace(ipqsKey),
		cached:  Report{Verdict: "off", FraudScore: -1},
	}
}

// Report returns the most recent snapshot. Lock-free-ish fast path.
func (c *Checker) Report() Report {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cached
}

// Run is the background refresh loop. Cancel ctx to stop.
func (c *Checker) Run(ctx context.Context) {
	c.refresh()
	ticker := time.NewTicker(RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refresh()
		}
	}
}

func (c *Checker) refresh() {
	rep := c.probeOnce()
	c.mu.Lock()
	c.cached = rep
	c.mu.Unlock()
}

// freshClient — like health's: every request opens a new TCP connection, so
// we never reuse a pre-tunnel socket and read a stale ISP IP.
func freshClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil},
	}
}

func (c *Checker) probeOnce() Report {
	rep := Report{FraudScore: -1}

	if c.running == nil || !c.running() {
		rep.Verdict = "off"
		rep.Message = "туннель выключен"
		return rep
	}
	rep.Running = true

	// 1) exit IP + geo/ASN/ISP (ip-api, free). Goes through the tunnel.
	if err := c.fillIPAPI(&rep); err != nil {
		rep.Verdict = "checking"
		rep.Message = "проверяю выход…"
		c.log.Logf("cleanliness: ip-api probe failed: %v", err)
		return rep
	}
	rep.Checked = true
	rep.CheckedAt = time.Now().UTC().Format(time.RFC3339)

	// 1b) ipapi.is — the real intelligence: datacenter/abuse/ASN-country.
	if err := c.fillIPApiIs(&rep); err != nil {
		c.log.Logf("cleanliness: ipapi.is probe failed: %v", err)
	}

	// 2) optional IPQS fraud-score (only if a key is configured AND funded).
	if c.ipqsKey != "" && rep.IP != "" {
		if err := c.fillIPQS(&rep); err != nil {
			c.log.Logf("cleanliness: ipqs probe skipped: %v", err)
		}
	}

	// 3) Cloudflare edge trace — which CF datacenter we hit + warp state.
	c.fillCFTrace(&rep)

	rep.Score, rep.Verdict, rep.Message, rep.Reasons = analyze(rep)
	return rep
}

// ----- individual probes ---------------------------------------------------

func (c *Checker) fillIPAPI(rep *Report) error {
	const fields = "status,message,countryCode,city,isp,org,as,asname,mobile,proxy,hosting,query"
	resp, err := freshClient(6 * time.Second).Get("http://ip-api.com/json/?fields=" + fields)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var j struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Country string `json:"countryCode"`
		City    string `json:"city"`
		ISP     string `json:"isp"`
		Org     string `json:"org"`
		As      string `json:"as"`
		ASName  string `json:"asname"`
		Mobile  bool   `json:"mobile"`
		Proxy   bool   `json:"proxy"`
		Hosting bool   `json:"hosting"`
		Query   string `json:"query"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return err
	}
	if j.Status != "success" {
		return fmt.Errorf("ip-api: %s", j.Message)
	}

	rep.IP = j.Query
	rep.ASN = j.As
	if sp := strings.IndexByte(j.As, ' '); sp > 0 {
		rep.ASN = j.As[:sp] // "AS210644 AEZA…" → "AS210644"
	}
	rep.ASName = j.ASName
	rep.ISP = j.ISP
	rep.Org = j.Org
	rep.Country = j.Country
	rep.City = j.City
	rep.IsHosting = j.Hosting
	rep.IsMobile = j.Mobile
	rep.IsProxy = j.Proxy
	return nil
}

// fillIPApiIs enriches with ipapi.is — the source that actually catches VPS
// providers (AEZA etc.) and abuse-feed membership. Merges flags (never
// downgrades): if either source says datacenter, it's datacenter.
func (c *Checker) fillIPApiIs(rep *Report) error {
	if rep.IP == "" {
		return fmt.Errorf("no ip yet")
	}
	resp, err := freshClient(8 * time.Second).Get("https://api.ipapi.is/?q=" + rep.IP)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var j struct {
		IsDatacenter bool `json:"is_datacenter"`
		IsVPN        bool `json:"is_vpn"`
		IsProxy      bool `json:"is_proxy"`
		IsTor        bool `json:"is_tor"`
		IsAbuser     bool `json:"is_abuser"`
		IsMobile     bool `json:"is_mobile"`
		IsCrawler    bool `json:"is_crawler"`
		Company      struct {
			Type        string `json:"type"`
			AbuserScore string `json:"abuser_score"` // "0.0586 (High)"
		} `json:"company"`
		Asn struct {
			Type        string `json:"type"`
			Country     string `json:"country"`
			AbuserScore string `json:"abuser_score"`
		} `json:"asn"`
		Abuse struct {
			Email string `json:"email"`
		} `json:"abuse"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return err
	}

	if j.IsDatacenter || j.Company.Type == "hosting" || j.Asn.Type == "hosting" {
		rep.IsHosting = true
	}
	if j.IsVPN {
		rep.IsVPN = true
		rep.IsProxy = true
	}
	if j.IsProxy {
		rep.IsProxy = true
	}
	if j.IsTor {
		rep.IsTor = true
	}
	if j.IsAbuser {
		rep.IsAbuser = true
	}
	if j.IsMobile {
		rep.IsMobile = true
	}
	if j.IsCrawler {
		rep.IsCrawler = true
	}
	if j.Asn.Type != "" {
		rep.ASNType = j.Asn.Type
	}
	rep.ASNCountry = strings.ToUpper(j.Asn.Country)
	if e := j.Abuse.Email; e != "" {
		rep.AbuseEmail = e
	}
	// Keep the worse of company/asn abuser label for display.
	rep.AbuserLabel = worseAbuserLabel(
		parseAbuserLabel(j.Company.AbuserScore),
		parseAbuserLabel(j.Asn.AbuserScore),
	)
	return nil
}

func (c *Checker) fillIPQS(rep *Report) error {
	url := fmt.Sprintf(
		"https://ipqualityscore.com/api/json/ip/%s/%s?strictness=1&allow_public_access_points=true",
		c.ipqsKey, rep.IP,
	)
	resp, err := freshClient(10 * time.Second).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var j struct {
		Success    bool   `json:"success"`
		Message    string `json:"message"`
		FraudScore int    `json:"fraud_score"`
		Proxy      bool   `json:"proxy"`
		VPN        bool   `json:"vpn"`
		Tor        bool   `json:"tor"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return err
	}
	if !j.Success {
		return fmt.Errorf("ipqs: %s", j.Message)
	}
	rep.FraudScore = j.FraudScore
	if j.Proxy {
		rep.IsProxy = true
	}
	if j.VPN {
		rep.IsVPN = true
		rep.IsProxy = true
	}
	if j.Tor {
		rep.IsTor = true
	}
	return nil
}

func (c *Checker) fillCFTrace(rep *Report) {
	resp, err := freshClient(6 * time.Second).Get("https://cloudflare.com/cdn-cgi/trace")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, line := range strings.Split(string(body), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "colo":
			rep.CFColo = v
		case "warp":
			rep.CFWarp = v
		}
	}
}

// ----- scoring + reasoning -------------------------------------------------

// abuserRank maps ipapi.is's textual label to an ordinal so we can compare.
var abuserRank = map[string]int{"": 0, "Low": 1, "Elevated": 2, "Medium": 2, "High": 3, "Very High": 4}

// parseAbuserLabel pulls "High" out of "0.0586 (High)".
func parseAbuserLabel(s string) string {
	l, r := strings.IndexByte(s, '('), strings.IndexByte(s, ')')
	if l >= 0 && r > l {
		return strings.TrimSpace(s[l+1 : r])
	}
	return ""
}

func worseAbuserLabel(a, b string) string {
	if abuserRank[a] >= abuserRank[b] {
		return a
	}
	return b
}

// analyze turns raw signals into a 0-100 whiteness index, a verdict bucket, a
// short message, and — the whole point — a list of concrete reasons the
// egress looks the way it does, each with a fix hint.
func analyze(rep Report) (int, string, string, []Reason) {
	if !rep.Checked {
		return 0, "checking", "проверяю выход…", nil
	}

	var reasons []Reason
	hasFraud := rep.FraudScore >= 0
	score := 100

	if hasFraud {
		score -= rep.FraudScore
		sev := "low"
		if rep.FraudScore >= 75 {
			sev = "high"
		} else if rep.FraudScore >= 40 {
			sev = "med"
		}
		reasons = append(reasons, Reason{sev,
			fmt.Sprintf("IPQS fraud-score %d/100", rep.FraudScore), ""})
	} else {
		// No authoritative fraud source → don't advertise a pristine 100.
		score -= 15
	}

	if rep.IsHosting {
		score -= 35
		reasons = append(reasons, Reason{"high",
			"Хостинг/датацентр IP — Cloudflare Turnstile почти всегда челленджит такие",
			"Перейти на residential или mobile выход — это устраняет главную причину фрода."})
	}
	if rep.IsAbuser || abuserRank[rep.AbuserLabel] >= 3 {
		score -= 25
		lbl := rep.AbuserLabel
		if lbl == "" {
			lbl = "да"
		}
		reasons = append(reasons, Reason{"high",
			"IP/подсеть в abuse-фидах (репутация: " + lbl + ")",
			"Сменить IP/подсеть — с этого сервера уже спамили/абузили, репутация прилипла к диапазону."})
	} else if abuserRank[rep.AbuserLabel] == 2 {
		score -= 12
		reasons = append(reasons, Reason{"med",
			"Умеренная abuse-репутация подсети (" + rep.AbuserLabel + ")",
			"Попробовать соседний IP/другой сервер того же провайдера."})
	}
	if rep.IsVPN || rep.IsProxy || rep.IsTor {
		score -= 20
		what := "proxy"
		if rep.IsTor {
			what = "Tor"
		} else if rep.IsVPN {
			what = "VPN"
		}
		reasons = append(reasons, Reason{"high",
			"IP публично помечен как " + what,
			"Использовать необъявленный протокол/порт и IP, не числящийся в VPN-списках."})
	}
	// ASN registered in a country CF treats with extra suspicion (RU/CN/IR…),
	// even when the IP geolocates elsewhere. AEZA is the textbook case: DE geo,
	// RU-registered ASN.
	if isHighRiskASNCountry(rep.ASNCountry) && rep.ASNCountry != rep.Country {
		score -= 8
		reasons = append(reasons, Reason{"med",
			fmt.Sprintf("ASN зарегистрирован в %s (гео IP — %s): CF повышает подозрительность",
				rep.ASNCountry, rep.Country),
			"Выбрать провайдера с ASN в той же «спокойной» юрисдикции, что и гео."})
	}
	if rep.IsCrawler {
		score -= 10
		reasons = append(reasons, Reason{"med", "IP в диапазоне известных ботов/краулеров", ""})
	}

	if rep.IsMobile {
		score += 10
		reasons = append(reasons, Reason{"good", "Мобильный оператор — такие IP читаются как очень чистые", ""})
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}

	// Verdict bucket — datacenter/proxy/abuser dominate, since those are
	// exactly what Cloudflare Turnstile keys off.
	var verdict string
	switch {
	case rep.IsHosting:
		verdict = "datacenter"
	case rep.IsProxy || rep.IsVPN || rep.IsTor || rep.IsAbuser:
		verdict = "flagged"
	case hasFraud && rep.FraudScore >= 50:
		verdict = "flagged"
	case hasFraud && rep.FraudScore >= 25:
		verdict = "suspicious"
	case hasFraud && score >= 75:
		verdict = "clean"
	case !hasFraud && score >= 80:
		// residential-looking AND no red flags from the free sources.
		verdict = "clean"
	default:
		verdict = "suspicious"
	}

	if verdict == "clean" && len(reasons) == 0 {
		reasons = append(reasons, Reason{"good",
			"Красных флагов не найдено — не датацентр, не в abuse-списках", ""})
	}

	loc := rep.Country
	if rep.ASName != "" {
		loc = rep.Country + " · " + rep.ASName
	}
	msg := map[string]string{
		"clean":      "чистый выход · " + loc,
		"datacenter": "датацентр · " + loc,
		"flagged":    "помечен · " + loc,
		"suspicious": "под вопросом · " + loc,
	}[verdict]
	if msg == "" {
		msg = loc
	}
	return score, verdict, msg, reasons
}

// isHighRiskASNCountry flags ASN home countries Cloudflare tends to treat with
// extra suspicion regardless of the IP's geolocation.
func isHighRiskASNCountry(cc string) bool {
	switch cc {
	case "RU", "CN", "IR", "KP", "BY", "SY":
		return true
	}
	return false
}
