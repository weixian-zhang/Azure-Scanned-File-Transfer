
package main

import (
	"github.com/weixian-zhang/ssftp/user"
	"time"
)

type Overlord struct {
	confsvc      *ConfigService
	clamav      ClamAv
	fileWatcher FileWatcher
	sftpservice *SFTPService
	usergov 	*user.UserGov
	httpClient HttpClient
}


func NewOverlord(confsvc *ConfigService, usergov *user.UserGov) (Overlord, error) {

	clamav := NewClamAvClient()

	proceed := make(chan bool)
	go proceedOnClamdConnect(clamav, proceed)

	//<- proceed //block until ssftp able to connect to Clamd on tcp://localhost:3310

	httpClient := NewHttpClient(confsvc)

	scanDone := make(chan bool)

	sftpsvc := NewSFTPService(confsvc, usergov)

	fw := NewFileWatcher(&sftpsvc, confsvc, usergov, scanDone)

	

	return Overlord{
		confsvc: confsvc,
		clamav: clamav,
		fileWatcher: fw,
		httpClient: httpClient,
		usergov: usergov,
		sftpservice: &sftpsvc,
		//usergov: ug,
	}, nil
}

func (ol *Overlord) Start(exit chan bool) {

	

	go ol.fileWatcher.startWatchConfigFileChange()

	go ol.fileWatcher.ScavengeUploadedFiles()

	go func() {

		for {

			select {

			case scavengedFile := <- ol.fileWatcher.fileCreateChangeEvent:

				logclient.Infof("Overlord - sending file %s for scanning from client %s", scavengedFile.Path, scavengedFile.User.Name)

				if !ol.confsvc.config.EnableVirusScan {

					logclient.Infof("Overlord - Virus scan is disabled")

					ol.fileWatcher.ScanDone <- true

				} else {
					go ol.clamav.ScanFile(scavengedFile.Path, scavengedFile.User)
				}

			//happens when clamd container is terminated or tcp connection can't be established
			case fileOnScan := <-ol.clamav.clamdError:

				logclient.Infof("Overlord - detects connectivity error to Clamd while scanning %s", fileOnScan)

			case scanR := <-ol.clamav.scanEvent:

				logclient.Infof("Overlord - scanning done for file %s from client %s and virus = %v", scanR.filePath, scanR.User.Name, scanR.VirusFound)

				destPath := ol.fileWatcher.moveFileByStatus(scanR)

				if scanR.VirusFound {

					ol.httpClient.callVirusFoundWebhook(VirusDetectedWebhookData{
						Username: scanR.User.Name,
						FileName: scanR.fileName,
						FilePath: destPath,
						ScanMessage: scanR.Message,
						TimeGenerated: (time.Now()).Format(time.ANSIC),
					})
				}				

			case <- exit:

					ol.fileWatcher.watcher.Close()
					logclient.Info("Overlord - exiting due to exit signal")
					
			}
		}
		
	}()

	ol.sftpservice.Start()
}

func proceedOnClamdConnect(clamav ClamAv, proceed chan bool) {
	for {
		_, err := clamav.PingClamd()

			if err != nil {
				logclient.Info("Overlord - sSFTP waiting for Clamd to be ready, connecting at tcp://localhost:3310")
				time.Sleep(3 * time.Second)
				continue
			} else {
				logclient.Info("Overlord - sSFTP connected to Clamd on tcp://localhost:3310")
				proceed <- true
				break
		}
	}
}

