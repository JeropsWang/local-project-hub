Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"

Name "Local Project Hub (Preview)"
OutFile "${OUTDIR}\LocalProjectHub-Setup-x64.exe"
InstallDir "$LOCALAPPDATA\Programs\Local Project Hub"
RequestExecutionLevel user
ShowInstDetails show

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "Windows x64 is required."
    Abort
  ${EndIf}
  ; Evergreen WebView2 registers its version in the 32-bit EdgeUpdate view.
  SetRegView 32
  ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ReadRegStr $0 HKLM "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${EndIf}
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    IfSilent missing
    MessageBox MB_ICONSTOP "Install Microsoft Edge WebView2 Evergreen Runtime before running Setup again."
    ExecShell "open" "https://developer.microsoft.com/microsoft-edge/webview2/"
    missing:
    SetErrorLevel 2
    Abort
  ${EndIf}
FunctionEnd

Section "Local Project Hub"
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  SetOverwrite on
  ClearErrors
  File "${OUTDIR}\LocalProjectHub.exe"
  IfErrors failed
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  IfErrors failed
  CreateShortcut "$SMPROGRAMS\Local Project Hub.lnk" "$INSTDIR\LocalProjectHub.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\LocalProjectHub" "DisplayName" "Local Project Hub (Preview)"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\LocalProjectHub" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
  Goto done
  failed:
  IfSilent +2
  MessageBox MB_ICONSTOP "Installation failed. Use Safe Exit in Local Project Hub and run Setup again."
  SetErrorLevel 1
  Abort
  done:
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  ; Remove only installer-owned paths. Workspaces may reside on any drive.
  ClearErrors
  Delete "$INSTDIR\LocalProjectHub.exe"
  IfErrors failed
  Delete "$SMPROGRAMS\Local Project Hub.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\LocalProjectHub"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Goto done
  failed:
  IfSilent +2
  MessageBox MB_ICONSTOP "Use Safe Exit in Local Project Hub before uninstalling."
  SetErrorLevel 1
  Abort
  done:
SectionEnd
