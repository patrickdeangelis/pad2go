; Pad2Go Windows installer. Built by packaging/build-windows-installer.sh:
;   makensis -DVERSION=1.2.3 -DDIST=<dir with pad2go.exe> -DVIGEMBUS=<ViGEmBus setup> -DOUTFILE=<installer>
; Installs pad2go.exe and, when no working ViGEmBus is found ("pad2go check"),
; the bundled ViGEmBus driver. The uninstaller leaves ViGEmBus in place: other
; apps may use it.

Unicode true
SetCompressor /SOLID lzma
RequestExecutionLevel admin

!include "MUI2.nsh"
!include "x64.nsh"

!define APP "Pad2Go"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Pad2Go"

Name "${APP}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\${APP}"
InstallDirRegKey HKLM "${UNINST_KEY}" "InstallLocation"
BrandingText "${APP} ${VERSION}"
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=0 "ProductName" "${APP}"
VIAddVersionKey /LANG=0 "FileDescription" "${APP} installer"
VIAddVersionKey /LANG=0 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=0 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=0 "LegalCopyright" "GPL-3.0"

!define MUI_ICON "${DIST}\pad2go.ico"
!define MUI_UNICON "${DIST}\pad2go.ico"
!define MUI_FINISHPAGE_RUN "$INSTDIR\pad2go.exe"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${DIST}\LICENSE.txt"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "PortugueseBR"
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "O Pad2Go requer o Windows de 64 bits."
    Abort
  ${EndIf}
  SetRegView 64
  SetShellVarContext all
FunctionEnd

Function un.onInit
  SetRegView 64
  SetShellVarContext all
FunctionEnd

Section "Pad2Go" SecApp
  SectionIn RO
  ; Close a running copy so pad2go.exe can be replaced.
  nsExec::Exec 'taskkill /f /im pad2go.exe'
  SetOutPath "$INSTDIR"
  File "${DIST}\pad2go.exe"
  File "${DIST}\LICENSE.txt"
  File "${DIST}\THIRD-PARTY-NOTICES.txt"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\pad2go.exe"

  WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${APP}"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "pad2go"
  WriteRegStr HKLM "${UNINST_KEY}" "URLInfoAbout" "https://github.com/patrickdeangelis/pad2go"
  WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\pad2go.exe"
  WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINST_KEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINST_KEY}" "NoRepair" 1
SectionEnd

; ViGEmBus: checked through the bus itself, not the service, since a
; registered service can lack its bus device (e.g. next to VirtualPad G2).
Section "ViGEmBus" SecViGEm
  DetailPrint "Verificando o driver ViGEmBus..."
  nsExec::ExecToLog '"$INSTDIR\pad2go.exe" check'
  Pop $0
  ${If} $0 == 0
    DetailPrint "ViGEmBus já está instalado."
    Return
  ${EndIf}
  DetailPrint "Instalando o driver ViGEmBus..."
  InitPluginsDir
  File "/oname=$PLUGINSDIR\ViGEmBus_Setup.exe" "${VIGEMBUS}"
  ExecWait '"$PLUGINSDIR\ViGEmBus_Setup.exe" /exenoui /qn /norestart' $0
  ${If} $0 == 3010
    SetRebootFlag true
  ${ElseIf} $0 != 0
    MessageBox MB_ICONEXCLAMATION "A instalação do ViGEmBus falhou (código $0).$\r$\nO Pad2Go funciona, mas sem controle Xbox virtual. Instale o ViGEmBus manualmente: https://github.com/nefarius/ViGEmBus/releases"
  ${EndIf}
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /f /im pad2go.exe'
  Delete "$SMPROGRAMS\${APP}.lnk"
  Delete "$INSTDIR\pad2go.exe"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\THIRD-PARTY-NOTICES.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKLM "${UNINST_KEY}"
SectionEnd
