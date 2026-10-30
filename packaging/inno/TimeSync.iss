; Installer for TimeSync (Inno Setup 6).
;
; Built by .github/workflows/release.yml:
;   ISCC /DAppVersion=2.0.0 /DArch=x64 /DSourceDir=<folder with the exes> TimeSync.iss
;
; It installs TimeSync.exe and ntp-sync.exe to C:\Program Files\TimeSync
; and, if ticked, sets up the background service. Uninstalling removes the
; service and restores the Windows Time service first.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef Arch
  #define Arch "x64"
#endif
#ifndef SourceDir
  #define SourceDir "..\..\dist\TimeSync-" + AppVersion + "-windows-" + Arch
#endif

[Setup]
; Never change the AppId: it is how Windows and winget recognize updates.
AppId={{8C3B1F0E-5A7D-4E21-9C4B-2F6D3A1E7B90}
AppName=TimeSync
AppVersion={#AppVersion}
AppVerName=TimeSync {#AppVersion}
AppPublisher=burnersen
AppPublisherURL=https://github.com/burnersen/TimeSync
AppSupportURL=https://github.com/burnersen/TimeSync/issues
AppUpdatesURL=https://github.com/burnersen/TimeSync/releases
VersionInfoVersion={#AppVersion}
; The service binary must live where only administrators can change it.
DefaultDirName={autopf}\TimeSync
DisableDirPage=yes
DisableProgramGroupPage=yes
PrivilegesRequired=admin
#if Arch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif
MinVersion=10.0
OutputBaseFilename=TimeSync-{#AppVersion}-setup-{#Arch}
SetupIconFile=..\..\gui\build\windows\icon.ico
UninstallDisplayIcon={app}\TimeSync.exe
UninstallDisplayName=TimeSync
LicenseFile=..\..\LICENSE
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
CloseApplications=yes

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"
Name: "de"; MessagesFile: "compiler:Languages\German.isl"

[CustomMessages]
en.TaskService=Keep the clock in sync in the background (Windows service, recommended)
de.TaskService=Uhr im Hintergrund synchron halten (Windows-Dienst, empfohlen)
en.TaskDesktop=Create a desktop shortcut
de.TaskDesktop=Verknüpfung auf dem Desktop anlegen
en.StatusService=Setting up the background service...
de.StatusService=Hintergrund-Dienst wird eingerichtet...
en.StatusStart=Starting the background service again...
de.StatusStart=Hintergrund-Dienst wird wieder gestartet...

[Tasks]
Name: "service"; Description: "{cm:TaskService}"
Name: "desktopicon"; Description: "{cm:TaskDesktop}"; Flags: unchecked

[Files]
Source: "{#SourceDir}\TimeSync.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\ntp-sync.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion
Source: "{#SourceDir}\THIRD_PARTY_NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\README.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\TimeSync"; Filename: "{app}\TimeSync.exe"
Name: "{autodesktop}\TimeSync"; Filename: "{app}\TimeSync.exe"; Tasks: desktopicon

[Run]
; "service install" is safe to repeat: on an update it refreshes the
; existing service and keeps its settings.
Filename: "{app}\ntp-sync.exe"; Parameters: "service install -nopause"; StatusMsg: "{cm:StatusService}"; Flags: runhidden waituntilterminated; Tasks: service
Filename: "{app}\ntp-sync.exe"; Parameters: "service start -nopause"; StatusMsg: "{cm:StatusStart}"; Flags: runhidden waituntilterminated; Check: RestartServiceAfterUpdate
Filename: "{app}\TimeSync.exe"; Description: "{cm:LaunchProgram,TimeSync}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\ntp-sync.exe"; Parameters: "service uninstall -nopause"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"

[Code]
var
  ServiceExisted: Boolean;

function ServiceInstalled(): Boolean;
begin
  Result := RegKeyExists(HKLM, 'SYSTEM\CurrentControlSet\Services\TimeSync');
end;

// The running service holds ntp-sync.exe open, so it is stopped before the
// files are replaced. "service install" (or the entry below) starts it again.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
  Exe: String;
begin
  Result := '';
  ServiceExisted := ServiceInstalled();
  Exe := ExpandConstant('{app}\ntp-sync.exe');
  if ServiceExisted and FileExists(Exe) then
    Exec(Exe, 'service stop -nopause', '', SW_HIDE, ewWaitUntilTerminated, Code);
end;

// Updating with the service box unticked must not leave a stopped service.
function RestartServiceAfterUpdate(): Boolean;
begin
  Result := ServiceExisted and (not WizardIsTaskSelected('service'));
end;
