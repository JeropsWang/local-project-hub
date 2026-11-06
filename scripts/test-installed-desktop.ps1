param(
    [Parameter(Mandatory)][string]$Executable,
    [Parameter(Mandatory)][string]$TestRoot,
    [Parameter(Mandatory)][string]$ScreenshotDir
)

$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Drawing

# The installer smoke invokes this in a clean Windows desktop session.
# UI Automation sends actions directly to the installed process's controls.
$process = Start-Process -FilePath $Executable -PassThru
try {
    function Wait-Element([string]$Name, [Windows.Automation.ControlType]$Type) {
        $condition = [Windows.Automation.AndCondition]::new(
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty, $Name),
            [Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ControlTypeProperty, $Type)
        )
        $until = [DateTime]::UtcNow.AddSeconds(35)
        do {
            $process.Refresh()
            if ($process.HasExited) { throw "Installed application exited with $($process.ExitCode)" }
            if ($process.MainWindowHandle -ne [IntPtr]::Zero) {
                try {
                    $window = [Windows.Automation.AutomationElement]::FromHandle($process.MainWindowHandle)
                    $element = $window.FindFirst([Windows.Automation.TreeScope]::Descendants, $condition)
                    if ($element) { return $element }
                } catch [Windows.Automation.ElementNotAvailableException] {
                    # WebView2 can replace the accessibility subtree while navigating.
                }
            }
            Start-Sleep -Milliseconds 250
        } while ([DateTime]::UtcNow -lt $until)
        throw "Installed window did not expose $Name ($Type). Session: $($process.SessionId), window: $($process.MainWindowHandle)"
    }

    function Invoke-Button([string]$Name) {
        $button = Wait-Element $Name ([Windows.Automation.ControlType]::Button)
        $button.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    }

    function Save-Window([string]$Name) {
        $window = [Windows.Automation.AutomationElement]::FromHandle($process.MainWindowHandle)
        $bounds = $window.Current.BoundingRectangle
        if ($bounds.Width -le 0 -or $bounds.Height -le 0) { throw 'Installed window has no drawable bounds' }
        $bitmap = [Drawing.Bitmap]::new([int]$bounds.Width, [int]$bounds.Height)
        try {
            $graphics = [Drawing.Graphics]::FromImage($bitmap)
            try {
                $graphics.CopyFromScreen([int]$bounds.Left, [int]$bounds.Top, 0, 0, $bitmap.Size)
                $bitmap.Save((Join-Path $ScreenshotDir "$Name.png"), [Drawing.Imaging.ImageFormat]::Png)
            } finally {
                $graphics.Dispose()
            }
        } finally {
            $bitmap.Dispose()
        }
    }

    New-Item -ItemType Directory -Force -Path $ScreenshotDir | Out-Null
    $field = Wait-Element '工作空间路径' ([Windows.Automation.ControlType]::Edit)
    Save-Window 'installed-setup'
    $path = Join-Path $TestRoot 'desktop-workspace'
    $field.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern).SetValue($path)
    Invoke-Button '创建工作空间'
    $null = Wait-Element '添加项目' ([Windows.Automation.ControlType]::Hyperlink)
    if (-not (Test-Path -LiteralPath (Join-Path $path 'workspace.json'))) {
        throw 'Installed application did not create a workspace on disk'
    }
    Save-Window 'installed-library'
    $settings = Wait-Element '设置' ([Windows.Automation.ControlType]::Hyperlink)
    $settings.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    Invoke-Button '安全退出'
    if (-not $process.WaitForExit(15000) -or $process.ExitCode -ne 0) {
        throw 'Installed application did not safely exit'
    }
    Write-Output 'Installed desktop UI passed setup, library, settings and safe exit.'
} finally {
    if (-not $process.HasExited) { Stop-Process -Id $process.Id }
}
