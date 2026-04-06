package top.miceworld.pocketbridge

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.launch
import top.miceworld.pocketbridge.bridge.BridgeService
import top.miceworld.pocketbridge.databinding.ActivityMainBinding

class MainActivity : ComponentActivity() {
    private lateinit var binding: ActivityMainBinding

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

        NotificationHelper.ensureChannels(this)
        requestNotificationPermissionIfNeeded()
        bindInitialConfig()
        bindActions()
        bindState()
    }

    private fun bindInitialConfig() {
        val config = BridgePrefs.load(this)
        binding.relayUrlInput.setText(config.relayUrl)
        binding.deviceIdInput.setText(config.deviceId)
        binding.tokenInput.setText(config.token)
        binding.notifyTargetInput.setText("laptop")
    }

    private fun bindActions() {
        binding.startButton.setOnClickListener {
            saveConfig()
            BridgeService.start(this)
        }
        binding.stopButton.setOnClickListener {
            BridgeService.stop(this)
        }
        binding.sendNotifyButton.setOnClickListener {
            saveConfig()
            BridgeService.sendNotify(
                context = this,
                target = binding.notifyTargetInput.text.toString().trim(),
                title = binding.notifyTitleInput.text.toString().trim(),
                body = binding.notifyBodyInput.text.toString().trim(),
            )
        }
    }

    private fun bindState() {
        lifecycleScope.launch {
            BridgeRuntime.state.collect { state ->
                val status = buildString {
                    append("状态：")
                    append(if (state.connected) "已连接" else "未连接")
                    if (state.deviceId.isNotBlank()) {
                        append(" / device=")
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
                binding.statusText.text = status
                binding.logText.text = state.logs.joinToString("\n")
            }
        }
    }

    private fun saveConfig() {
        BridgePrefs.save(
            this,
            BridgeConfig(
                relayUrl = binding.relayUrlInput.text.toString().trim(),
                deviceId = binding.deviceIdInput.text.toString().trim(),
                token = binding.tokenInput.text.toString().trim(),
            ),
        )
    }

    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }
}
