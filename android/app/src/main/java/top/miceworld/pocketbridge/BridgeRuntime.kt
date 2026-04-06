package top.miceworld.pocketbridge

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

data class UiState(
    val connected: Boolean = false,
    val deviceId: String = "",
    val relayUrl: String = "",
    val lastError: String = "",
    val logs: List<String> = emptyList(),
)

object BridgeRuntime {
    private val _state = MutableStateFlow(UiState())
    val state = _state.asStateFlow()

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
}
