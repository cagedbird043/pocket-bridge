package top.miceworld.pocketbridge

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.os.Build
import android.os.Bundle
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.isVisible
import androidx.core.view.updatePadding
import androidx.lifecycle.lifecycleScope
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import kotlinx.coroutines.launch
import top.miceworld.pocketbridge.bridge.BridgeService
import top.miceworld.pocketbridge.databinding.ActivityMainBinding

class MainActivity : ComponentActivity() {
    private lateinit var binding: ActivityMainBinding

    private val timeFormatter: DateTimeFormatter = DateTimeFormatter.ofPattern("MM-dd HH:mm", Locale.getDefault())
        .withZone(ZoneId.systemDefault())

    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        if (!granted) {
            BridgeRuntime.appendLog("通知权限未授予")
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)
        applySystemBarAppearance()
        applyWindowInsets()

        BridgeRuntime.ensureLoaded(this)
        NotificationHelper.ensureChannels(this)
        PushBridge.fetchAndStoreToken(this)
        applyProvisionIntent(intent)
        requestNotificationPermissionIfNeeded()
        bindToolbar()
        bindTargetDropdown()
        bindInitialState()
        bindBottomNavigation(savedInstanceState?.getInt(KEY_SELECTED_TAB) ?: R.id.nav_recent)
        bindActions()
        bindState()
    }

    override fun onResume() {
        super.onResume()
        bindInitialState()
    }

    override fun onPause() {
        saveConfigFromFields()
        super.onPause()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        applyProvisionIntent(intent)
        if (::binding.isInitialized) {
            bindInitialState()
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putInt(KEY_SELECTED_TAB, binding.bottomNavigation.selectedItemId)
    }


    private fun applyWindowInsets() {
        val toolbarTop = binding.topToolbar.paddingTop
        val bottomNavBottom = binding.bottomNavigation.paddingBottom
        ViewCompat.setOnApplyWindowInsetsListener(binding.root) { _, insets ->
            val systemBars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
            binding.topToolbar.updatePadding(top = toolbarTop + systemBars.top)
            binding.bottomNavigation.updatePadding(bottom = bottomNavBottom + systemBars.bottom)
            insets
        }
        ViewCompat.requestApplyInsets(binding.root)
    }

    private fun applySystemBarAppearance() {
        val lightBars = (resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) != Configuration.UI_MODE_NIGHT_YES
        WindowCompat.getInsetsController(window, window.decorView).apply {
            isAppearanceLightStatusBars = lightBars
            isAppearanceLightNavigationBars = lightBars
        }
    }

    private fun bindToolbar() {
        binding.topToolbar.title = getString(R.string.app_name)
    }

    private fun bindTargetDropdown() {
        binding.notifyTargetInput.setOnItemClickListener { _, _, _, _ ->
            saveConfigFromFields()
        }
    }

    private fun bindBottomNavigation(initialTab: Int) {
        binding.bottomNavigation.setOnItemSelectedListener { item ->
            showTab(item.itemId)
            true
        }
        binding.bottomNavigation.selectedItemId = initialTab
        showTab(initialTab)
    }

    private fun bindInitialState() {
        val config = BridgePrefs.load(this)
        val targetOptions = buildTargetOptions(
            deviceId = config.deviceId,
            selectedTarget = config.notifyTarget,
        )
        val resolvedTarget = resolveDefaultNotifyTarget(config)
        if (resolvedTarget != config.notifyTarget) {
            BridgePrefs.save(this, config.copy(notifyTarget = resolvedTarget))
        }
        BridgeRuntime.syncConfig(config.copy(notifyTarget = resolvedTarget))
        binding.notifyTargetInput.setSimpleItems(targetOptions.toTypedArray())
        binding.notifyTargetInput.setText(resolvedTarget, false)
        binding.relayUrlInput.setText(config.relayUrl)
        binding.deviceIdInput.setText(config.deviceId)
        binding.privateKeyInput.setText(config.privateKeyBase64)
        renderActionTarget(resolvedTarget)
    }

    private fun bindActions() {
        binding.saveButton.setOnClickListener {
            saveConfigFromFields()
            BridgeRuntime.appendLog("已保存连接配置")
        }
        binding.startButton.setOnClickListener {
            saveConfigFromFields()
            BridgeService.start(this)
        }
        binding.stopButton.setOnClickListener {
            BridgeService.stop(this)
        }
        binding.sendNotifyButton.setOnClickListener {
            saveConfigFromFields()
            BridgeService.sendNotify(
                context = this,
                target = binding.notifyTargetInput.text?.toString().orEmpty().trim(),
                title = binding.notifyTitleInput.text?.toString().orEmpty().trim(),
                body = binding.notifyBodyInput.text?.toString().orEmpty().trim(),
            )
        }
        binding.readClipboardButton.setOnClickListener {
            binding.clipboardDraftInput.setText(readLocalClipboardText())
            BridgeRuntime.appendLog("已读取本机剪贴板到草稿区")
        }
        binding.writeClipboardButton.setOnClickListener {
            writeLocalClipboardText(binding.clipboardDraftInput.text?.toString().orEmpty())
            BridgeRuntime.appendLog("已将草稿区写入本机剪贴板")
        }
        binding.pushClipboardButton.setOnClickListener {
            saveConfigFromFields()
            BridgeService.pushClipboard(
                context = this,
                target = binding.notifyTargetInput.text?.toString().orEmpty().trim(),
            )
        }
        binding.pullClipboardButton.setOnClickListener {
            saveConfigFromFields()
            BridgeService.pullClipboard(
                context = this,
                target = binding.notifyTargetInput.text?.toString().orEmpty().trim(),
            )
        }
    }

    private fun bindState() {
        lifecycleScope.launch {
            BridgeRuntime.state.collect { state ->
                renderConnectionState(state)
                renderDiagnostics(state)
                renderSettingsSummary(state)
                renderRecentActivities(state.recentActivities)
            }
        }
    }

    private fun showTab(itemId: Int) {
        binding.recentTabScroll.isVisible = itemId == R.id.nav_recent
        binding.actionsTabScroll.isVisible = itemId == R.id.nav_actions
        binding.settingsTabScroll.isVisible = itemId == R.id.nav_settings
        binding.topToolbar.subtitle = when (itemId) {
            R.id.nav_actions -> getString(R.string.toolbar_subtitle_actions)
            R.id.nav_settings -> getString(R.string.toolbar_subtitle_settings)
            else -> getString(R.string.toolbar_subtitle_recent)
        }
    }

    private fun renderConnectionState(state: UiState) {
        binding.connectionStateText.text = if (state.connected) {
            getString(R.string.status_connected_short)
        } else {
            getString(R.string.status_disconnected_short)
        }
        binding.connectionSummaryText.text = if (state.connected) {
            getString(R.string.home_status_connected_summary)
        } else {
            getString(R.string.home_status_disconnected_summary)
        }
        binding.connectionMetaText.text = buildString {
            if (state.deviceId.isNotBlank()) {
                append("device=")
                append(state.deviceId)
            }
            if (state.relayUrl.isNotBlank()) {
                if (isNotEmpty()) {
                    append('\n')
                }
                append("relay=")
                append(state.relayUrl)
            }
            if (state.lastError.isNotBlank()) {
                if (isNotEmpty()) {
                    append('\n')
                }
                append("lastError=")
                append(state.lastError)
            }
        }
        binding.connectionMetaText.isVisible = binding.connectionMetaText.text.isNotBlank()
    }

    private fun renderDiagnostics(state: UiState) {
        binding.diagnosticSummaryText.text = when {
            state.lastError.isNotBlank() -> getString(R.string.diagnostic_last_error, state.lastError)
            state.logs.isNotEmpty() -> getString(R.string.diagnostic_last_log, state.logs.last())
            else -> getString(R.string.diagnostic_stable)
        }
    }

    private fun renderSettingsSummary(state: UiState) {
        binding.statusText.text = buildString {
            append(if (state.connected) getString(R.string.status_connected_short) else getString(R.string.status_disconnected_short))
            if (state.deviceId.isNotBlank()) {
                append(" · device=")
                append(state.deviceId)
            }
            if (state.relayUrl.isNotBlank()) {
                append("\nrelay=")
                append(state.relayUrl)
            }
            if (state.lastError.isNotBlank()) {
                append("\nlastError=")
                append(state.lastError)
            }
        }
        binding.logText.text = state.logs.takeLast(30).reversed().joinToString("\n")
            .ifBlank { getString(R.string.settings_logs_empty) }
    }

    private fun renderRecentActivities(entries: List<RecentActivityEntry>) {
        binding.recentActivityContainer.removeAllViews()
        val hasEntries = entries.isNotEmpty()
        binding.recentActivityEmptyText.isVisible = !hasEntries
        binding.recentActivityEmptyHintText.isVisible = !hasEntries
        binding.recentActivityContainer.isVisible = hasEntries
        if (!hasEntries) {
            return
        }

        entries.forEach { entry ->
            val itemView = layoutInflater.inflate(
                R.layout.item_recent_activity,
                binding.recentActivityContainer,
                false,
            )
            itemView.findViewById<TextView>(R.id.activityKindText).text = buildActivityKindLabel(entry)
            itemView.findViewById<TextView>(R.id.activityTimestampText).text = formatTimestamp(entry.timestampMs)
            itemView.findViewById<TextView>(R.id.activityTitleText).text = entry.title

            val bodyView = itemView.findViewById<TextView>(R.id.activityBodyText)
            bodyView.text = entry.body
            bodyView.isVisible = entry.body.isNotBlank()

            val metaView = itemView.findViewById<TextView>(R.id.activityMetaText)
            metaView.text = buildActivityMeta(entry)
            metaView.isVisible = metaView.text.isNotBlank()

            binding.recentActivityContainer.addView(itemView)
        }
    }

    private fun buildActivityKindLabel(entry: RecentActivityEntry): String {
        val primary = when (entry.kind) {
            RecentActivityEntry.KIND_TASK -> getString(R.string.recent_activity_kind_task)
            else -> getString(R.string.recent_activity_kind_notify)
        }
        val ingress = when (entry.ingress) {
            RecentActivityEntry.INGRESS_FCM -> getString(R.string.recent_activity_ingress_fcm)
            else -> getString(R.string.recent_activity_ingress_websocket)
        }
        return primary + getString(R.string.recent_activity_meta_joiner) + ingress
    }

    private fun buildActivityMeta(entry: RecentActivityEntry): String {
        return if (entry.fromDeviceId.isBlank()) {
            ""
        } else {
            getString(R.string.recent_activity_from, entry.fromDeviceId)
        }
    }


    private fun buildTargetOptions(deviceId: String, selectedTarget: String): List<String> {
        val inferredPeer = inferPeerTarget(deviceId)
        return listOf(
            selectedTarget.trim(),
            inferredPeer,
            "laptop",
            "phone",
        ).filter { it.isNotBlank() }
            .distinct()
    }

    private fun resolveDefaultNotifyTarget(config: BridgeConfig): String {
        return config.notifyTarget.trim().ifBlank { inferPeerTarget(config.deviceId) }
    }

    private fun inferPeerTarget(deviceId: String): String {
        return if (deviceId.trim().equals("laptop", ignoreCase = true)) {
            "phone"
        } else {
            "laptop"
        }
    }

    private fun renderActionTarget(target: String) {
        binding.actionsTargetText.text = if (target.isBlank()) {
            getString(R.string.actions_target_summary_missing)
        } else {
            getString(R.string.actions_target_summary, target)
        }
    }

    private fun formatTimestamp(timestampMs: Long): String {
        return runCatching {
            timeFormatter.format(Instant.ofEpochMilli(timestampMs))
        }.getOrElse { "" }
    }

    private fun saveConfigFromFields(): BridgeConfig {
        val draft = BridgeConfig(
            relayUrl = binding.relayUrlInput.text?.toString().orEmpty().trim(),
            deviceId = binding.deviceIdInput.text?.toString().orEmpty().trim(),
            privateKeyBase64 = binding.privateKeyInput.text?.toString().orEmpty().trim(),
            notifyTarget = binding.notifyTargetInput.text?.toString().orEmpty().trim(),
        )
        val next = draft.copy(notifyTarget = resolveDefaultNotifyTarget(draft))
        binding.notifyTargetInput.setSimpleItems(
            buildTargetOptions(deviceId = next.deviceId, selectedTarget = next.notifyTarget).toTypedArray(),
        )
        binding.notifyTargetInput.setText(next.notifyTarget, false)
        BridgePrefs.save(this, next)
        BridgeRuntime.syncConfig(next)
        renderActionTarget(next.notifyTarget)
        return next
    }

    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    private fun readLocalClipboardText(): String {
        val manager = getSystemService(ClipboardManager::class.java) ?: return ""
        val clip = manager.primaryClip ?: return ""
        if (clip.itemCount == 0) {
            return ""
        }
        return clip.getItemAt(0).coerceToText(this)?.toString().orEmpty()
    }

    private fun writeLocalClipboardText(text: String) {
        val manager = getSystemService(ClipboardManager::class.java) ?: return
        manager.setPrimaryClip(ClipData.newPlainText("pocket-bridge", text))
    }

    private fun applyProvisionIntent(intent: Intent?) {
        if (intent == null || !intent.hasProvisionPayload()) {
            return
        }

        val current = BridgePrefs.load(this)
        val next = BridgeConfig(
            relayUrl = intent.getStringExtra(EXTRA_RELAY_URL)?.trim().takeUnless { it.isNullOrEmpty() } ?: current.relayUrl,
            deviceId = intent.getStringExtra(EXTRA_DEVICE_ID)?.trim().takeUnless { it.isNullOrEmpty() } ?: current.deviceId,
            privateKeyBase64 = intent.getStringExtra(EXTRA_PRIVATE_KEY_BASE64)?.trim().takeUnless { it.isNullOrEmpty() } ?: current.privateKeyBase64,
            notifyTarget = intent.getStringExtra(EXTRA_NOTIFY_TARGET)?.trim().takeUnless { it.isNullOrEmpty() } ?: current.notifyTarget,
        )
        BridgePrefs.save(this, next)
        BridgeRuntime.syncConfig(next)

        if (intent.getBooleanExtra(EXTRA_AUTO_START, false)) {
            BridgeService.restart(this)
        }

        BridgeRuntime.appendLog(
            "已应用 adb provision: device=${next.deviceId} relay=${next.relayUrl} target=${next.notifyTarget}",
        )

        if (intent.getBooleanExtra(EXTRA_FINISH_AFTER_PROVISION, false)) {
            finish()
        }
    }

    private fun Intent.hasProvisionPayload(): Boolean {
        return action == ACTION_PROVISION ||
            hasExtra(EXTRA_RELAY_URL) ||
            hasExtra(EXTRA_DEVICE_ID) ||
            hasExtra(EXTRA_PRIVATE_KEY_BASE64) ||
            hasExtra(EXTRA_NOTIFY_TARGET) ||
            hasExtra(EXTRA_AUTO_START)
    }

    companion object {
        private const val KEY_SELECTED_TAB = "selected_tab"

        const val ACTION_PROVISION = "top.miceworld.pocketbridge.action.PROVISION"
        const val EXTRA_RELAY_URL = "relay_url"
        const val EXTRA_DEVICE_ID = "device_id"
        const val EXTRA_PRIVATE_KEY_BASE64 = "private_key_base64"
        const val EXTRA_NOTIFY_TARGET = "notify_target"
        const val EXTRA_AUTO_START = "auto_start"
        const val EXTRA_FINISH_AFTER_PROVISION = "finish_after_provision"
    }
}
