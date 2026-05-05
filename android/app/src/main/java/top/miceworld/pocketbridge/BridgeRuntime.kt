package top.miceworld.pocketbridge

import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

data class UiState(
    val connected: Boolean = false,
    val deviceId: String = "",
    val relayUrl: String = "",
    val lastError: String = "",
    val logs: List<String> = emptyList(),
    val recentActivities: List<RecentActivityEntry> = emptyList(),
)

object BridgeRuntime {
    private val _state = MutableStateFlow(UiState())
    val state = _state.asStateFlow()

    private var recentActivitiesLoaded = false

    fun ensureLoaded(context: Context) {
        if (recentActivitiesLoaded) {
            return
        }
        _state.value = _state.value.copy(
            recentActivities = RecentActivityStore.load(context),
        )
        recentActivitiesLoaded = true
    }

    fun syncConfig(config: BridgeConfig) {
        _state.value = _state.value.copy(
            deviceId = config.deviceId,
            relayUrl = config.relayUrl,
        )
    }

    fun updateConnection(connected: Boolean, deviceId: String, relayUrl: String, lastError: String = "") {
        _state.value = _state.value.copy(
            connected = connected,
            deviceId = deviceId,
            relayUrl = relayUrl,
            lastError = lastError,
        )
    }

    fun appendLog(line: String) {
        val current = _state.value.logs.takeLast(59)
        _state.value = _state.value.copy(logs = current + line)
    }

    fun recordIncomingNotify(
        context: Context,
        title: String,
        body: String,
        fromDeviceId: String,
        ingress: String,
        timestampMs: Long,
    ) {
        appendRecentActivity(
            context = context,
            entry = RecentActivityEntry.notify(
                title = title,
                body = body,
                fromDeviceId = fromDeviceId,
                ingress = ingress,
                timestampMs = timestampMs,
            ),
        )
    }

    fun recordIncomingTask(
        context: Context,
        taskKind: String,
        title: String,
        summary: String,
        fromDeviceId: String,
        ingress: String,
        timestampMs: Long,
    ) {
        appendRecentActivity(
            context = context,
            entry = RecentActivityEntry.task(
                taskKind = taskKind,
                title = title,
                summary = summary,
                fromDeviceId = fromDeviceId,
                ingress = ingress,
                timestampMs = timestampMs,
            ),
        )
    }

    private fun appendRecentActivity(context: Context, entry: RecentActivityEntry) {
        ensureLoaded(context)
        _state.value = _state.value.copy(
            recentActivities = RecentActivityStore.append(context, entry),
        )
    }
}
