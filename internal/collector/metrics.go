package collector

import "github.com/prometheus/client_golang/prometheus"

const ns = "openldap"

func desc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(ns+"_"+name, help, labels, nil)
}

// tl are the labels every per-target metric carries.
var tl = []string{"target", "group", "role"}

func withTL(extra ...string) []string { return append(append([]string{}, tl...), extra...) }

var (
	// ---- scrape health
	dUp             = desc("up", "1 if the target answered a bind (or anonymous connect) during this scrape.", tl...)
	dScrapeDuration = desc("scrape_duration_seconds", "Wall time of the whole scrape of this target.", tl...)
	dScrapeError    = desc("scrape_error", "1 for each scrape phase that failed. Absent when the phase succeeded.", withTL("phase")...)
	dDialSeconds    = desc("dial_duration_seconds", "TCP connect plus optional StartTLS.", tl...)
	dBindSeconds    = desc("bind_duration_seconds", "Simple bind round-trip.", tl...)
	dSearchSeconds  = desc("search_duration_seconds", "Round-trip of each search the exporter ran, by search name.", withTL("search")...)

	// ---- info
	dServerInfo = desc("server_info", "Server version string from cn=Monitor.", withTL("version")...)
	dBuildInfo  = desc("exporter_build_info", "Exporter build metadata.", "version", "revision", "goversion")

	// ---- cn=Monitor: connections, operations, statistics
	dConnTotal   = desc("connections_total", "Connections accepted since start (cn=Total,cn=Connections).", tl...)
	dConnCurrent = desc("connections_current", "Open connections right now.", tl...)
	dOpsInit     = desc("operations_initiated_total", "Operations started, by type.", withTL("operation")...)
	dOpsDone     = desc("operations_completed_total", "Operations finished, by type.", withTL("operation")...)
	dBytes       = desc("statistics_bytes_total", "Bytes sent to clients.", tl...)
	dPDU         = desc("statistics_pdu_total", "Protocol data units sent.", tl...)
	dEntriesSent = desc("statistics_entries_total", "Entries returned to clients.", tl...)
	dReferrals   = desc("statistics_referrals_total", "Referrals returned to clients.", tl...)

	// ---- threads, waiters
	dThreads     = desc("threads", "Thread pool figures from cn=Threads, by kind (max, active, open, pending, backload, ...).", withTL("kind")...)
	dThreadState = desc("thread_state_info", "Thread pool state string.", withTL("state")...)
	dWaiters     = desc("waiters", "Threads waiting on I/O, by direction.", withTL("direction")...)

	// ---- time
	dStartTime  = desc("start_time_seconds", "slapd start time, Unix seconds.", tl...)
	dServerTime = desc("server_time_seconds", "slapd's own clock at scrape, Unix seconds. Compare across targets for skew.", tl...)
	dUptime     = desc("uptime_seconds", "Seconds since slapd started.", tl...)
	dClockOff   = desc("clock_offset_seconds", "server_time minus the exporter's wall clock. Includes network latency.", tl...)

	// ---- databases (cn=Monitor)
	dDBInfo         = desc("database_info", "One per database in cn=Databases,cn=Monitor.", withTL("database", "suffix", "backend")...)
	dDBShadow       = desc("database_shadow", "1 if the database is a shadow (pure consumer).", withTL("database", "suffix")...)
	dMDBPagesMax    = desc("mdb_pages_max", "LMDB map size in pages (olmMDBPagesMax).", withTL("database", "suffix")...)
	dMDBPagesUsed   = desc("mdb_pages_used", "LMDB pages in use.", withTL("database", "suffix")...)
	dMDBPagesFree   = desc("mdb_pages_free", "LMDB pages on the free list.", withTL("database", "suffix")...)
	dMDBPagesRatio  = desc("mdb_pages_used_ratio", "pages_used / pages_max. Alert before 0.9: LMDB fails hard at 1.0.", withTL("database", "suffix")...)
	dMDBReadersMax  = desc("mdb_readers_max", "LMDB reader slots.", withTL("database", "suffix")...)
	dMDBReadersUsed = desc("mdb_readers_used", "LMDB reader slots in use.", withTL("database", "suffix")...)
	dMDBEntries     = desc("mdb_entries", "Entries in the LMDB database as reported by back-monitor.", withTL("database", "suffix")...)
	dOverlayInfo    = desc("overlay_info", "One per loaded overlay.", withTL("overlay")...)

	// ---- replication (from contextCSN)
	dCSNTime     = desc("context_csn_timestamp_seconds", "contextCSN timestamp per sid, Unix seconds.", withTL("suffix", "sid")...)
	dCSNSIDs     = desc("context_csn_sids", "Number of distinct sids in this suffix's contextCSN.", withTL("suffix")...)
	dReplLag     = desc("replication_lag_seconds", "Seconds this replica is behind the newest value of this sid within its group. 0 when current.", withTL("suffix", "sid")...)
	dReplMissing = desc("replication_sid_missing", "1 if another group member carries this sid and this replica does not.", withTL("suffix", "sid")...)
	dReplInSync  = desc("replication_in_sync", "1 if every sid is within replication.in_sync_tolerance of the group's newest value.", withTL("suffix")...)
	dGroupMaxLag = desc("replication_group_max_lag_seconds", "Worst lag across all members of the group for this suffix.", "group", "suffix")
	dGroupInSync = desc("replication_group_in_sync", "1 if every member of the group is in sync for this suffix.", "group", "suffix")

	// ---- cn=config
	dCfgReadable       = desc("config_readable", "1 if cn=config was read this scrape.", tl...)
	dCfgAge            = desc("config_scrape_age_seconds", "Age of the cn=config data behind the config metrics. 0 means it was read during this scrape; otherwise it was served from the cache, which refreshes every config_interval.", tl...)
	dServerID          = desc("server_id", "olcServerID.", tl...)
	dLogLevelInfo      = desc("log_level_info", "olcLogLevel as configured. Replication is invisible in logs unless it includes 'sync'.", withTL("level")...)
	dDBMultiProv       = desc("database_multiprovider", "1 if olcMultiProvider (2.5+) or olcMirrorMode (2.4) is TRUE.", withTL("suffix", "backend")...)
	dDBReadOnly        = desc("database_readonly", "1 if olcReadOnly is TRUE. Blocks all client writes, including on a consumer.", withTL("suffix", "backend")...)
	dDBMaxSize         = desc("database_max_size_bytes", "olcDbMaxSize.", withTL("suffix", "backend")...)
	dSpOverlays        = desc("syncprov_overlays", "Number of syncprov overlays on this database. Must be exactly 1 for a provider.", withTL("suffix")...)
	dSpSessionlog      = desc("syncprov_sessionlog", "olcSpSessionlog. How many changes a provider can replay before falling back to a full present phase.", withTL("suffix")...)
	dConsumers         = desc("syncrepl_consumers", "Number of olcSyncRepl statements on this target.", tl...)
	dConsumerInfo      = desc("syncrepl_consumer_info", "One per olcSyncRepl statement.", withTL("rid", "provider", "provider_host", "suffix", "type", "bindmethod")...)
	dConsumerKA        = desc("syncrepl_consumer_keepalive", "1 if the statement sets keepalive=. Without it an idle refreshAndPersist link across a firewall can be silently blackholed.", withTL("rid", "provider_host")...)
	dConsumerRetry     = desc("syncrepl_consumer_retry_forever", "1 if retry ends with '+'. Without it retries stop for good after the list is exhausted.", withTL("rid", "provider_host")...)
	dConsumerSelf      = desc("syncrepl_consumer_self_reference", "1 if the provider is this same host - a consumer pointing at itself.", withTL("rid", "provider_host")...)
	dConsumerProvGroup = desc("syncrepl_consumer_provider_group", "1, labelled with the group of the provider when the provider is also a scraped target. Alert on group=prod,provider_group=qa to enforce one-way replication.", withTL("rid", "provider_host", "provider_group")...)

	// ---- provider-side link presence
	// link_id is consumer+"-"+rid, guaranteed unique because rid only needs
	// to be unique within one consumer's own syncrepl statements (an LDAP
	// requirement) and consumer (the exporter's target name) is already
	// enforced unique config-wide (internal/config.finish). It exists so a
	// topology graph (e.g. a Grafana Node Graph panel) has a stable, collision
	// -free edge id without reconstructing one via string-matching in PromQL.
	dLinkSeen = desc("syncrepl_link_seen_on_provider", "1 if the provider currently holds a connection from this consumer's address. Read on the provider, so it reflects reality rather than the consumer's opinion.", "consumer", "provider", "rid", "group", "link_id")

	// ---- entry counts
	dEntries = desc("entries", "Entry count of a configured search.", withTL("name", "base")...)

	// ---- TLS
	dTLSExpiry = desc("tls_certificate_expiry_seconds", "NotAfter of the server certificate, Unix seconds.", tl...)
	dTLSInfo   = desc("tls_certificate_info", "Issuer of the server certificate.", withTL("issuer")...)
)
