package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/souvikhaldar/trafczar/config"
	mongodb "github.com/souvikhaldar/trafczar/db"

	"github.com/spf13/cobra"
	"go.mongodb.org/mongo-driver/bson"
)

type Response struct {
	Status      string  `json:"status"`
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"region"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	Zip         string  `json:"zip"`
	Lat         float32 `json:"lat"`
	Lon         float32 `json:"lon"`
	Timezone    string  `json:"timezone"`
	ISP         string  `json:"isp"`
	ORG         string  `json:"org"`
	AS          string  `json:"as"`
	Query       string  `json:"query"`
	Hits        int32   `json:"hits"`
}

var tcpDump string
var ipAdd string
var readStream bool
var conf string
var persist bool
var port string

func init() {
	rootCmd.AddCommand(ipCmd)
	ipCmd.Flags().StringVarP(&tcpDump, "tcp-dump", "t", "", "source file of tcpdump")
	ipCmd.Flags().StringVar(&ipAdd, "ip", "", "IP address of the target")
	ipCmd.Flags().BoolVarP(&readStream, "read-stream", "s", false, "Do you want to read from tcpdump output contineously?")
	ipCmd.Flags().StringVar(&port, "port", "80", "Port address to listen on")
	ipCmd.PersistentFlags().StringVarP(&conf, "config", "c", "", "The path to the configuration JSON file")
	ipCmd.Flags().BoolVarP(&persist, "persist", "p", false, "Do you want to store the response to mongo? If yes, please provide value to --config flag")
}

var ipCmd = &cobra.Command{
	Use:   "ipinfo",
	Short: "Fetch the location information of the IP",
	Run: func(cmd *cobra.Command, args []string) {
		var con config.Config
		if persist {
			con = config.SetEnv(conf)
			if err := mongodb.InitializeMongoDB(con); err != nil {
				log.Fatal(err)
			}
		}

		ipCache := make(map[string]*Response)
		if readStream {
			var cmd *exec.Cmd
			if port == "80" {
				cmd = exec.Command("sh", "-c", "sudo tcpdump -i any port 80 | cut -d ' ' -f 5")
			} else {
				cmd = exec.Command("sh", "-c", "sudo tcpdump -i any port 443 | grep 'In' | cut -d ' ' -f 7")
			}
			stdOut, err := cmd.StdoutPipe()
			if err != nil {
				fmt.Println(err)
				return
			}
			scanner := bufio.NewScanner(stdOut)
			go func() {
				for scanner.Scan() {
					var response Response
					ip, err := ParseIPFromTcpDump(scanner.Text())
					if err != nil || len(ip) == 0 {
						continue
					}

					// Cache hit
					if cacheRes, ok := ipCache[ip]; ok {
						cacheRes.Hits += 1

						if !persist || cacheRes.Status == "fail" || len(cacheRes.Status) == 0 {
							continue
						}

						_, err := mongodb.MongoIPCollection.UpdateOne(
							context.TODO(),
							bson.D{
								{"query", cacheRes.Query},
							},
							bson.D{
								{
									"$inc", bson.D{
										{"hits", 1},
									},
								},
							},
						)
						if err != nil {
							fmt.Println("Could not update: ", err)
							fmt.Println("--------------------------------------------------------------------------------")
							continue
						}
						continue
					}

					response, err = getIPInfo(ip)
					if err != nil {
						fmt.Println(err)
						fmt.Println("--------------------------------------------------------------------------------")
						continue
					}
					// insert to cache
					ipCache[ip] = &response
					fmt.Printf(
						"Request coming from:\n %+v \n",
						response)

					if !persist || response.Status == "fail" || len(response.Status) == 0 {
						fmt.Println("--------------------------------------------------------------------------------")
						continue
					}

					updateRes, err := mongodb.MongoIPCollection.UpdateOne(
						context.TODO(),
						bson.D{
							{"query", response.Query},
						},
						bson.D{
							{
								"$inc", bson.D{
									{"hits", 1},
								},
							},
						},
					)
					if err != nil {
						fmt.Println("Could not update: ", err)
						fmt.Println("--------------------------------------------------------------------------------")
						continue
					}
					if updateRes.UpsertedCount > 0 {
						continue
						fmt.Println("--------------------------------------------------------------------------------")
					}

					response.Hits = 1
					if _, err := mongodb.MongoIPCollection.InsertOne(
						context.TODO(),
						response,
					); err != nil {
						fmt.Println("error in inserting to mongo: ", err)
					}
					fmt.Println("--------------------------------------------------------------------------------")
				}
			}()
			if err := cmd.Start(); err != nil {
				log.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				log.Fatal(err)
			}

		}

		if tcpDump != "" {
			f, err := os.Open(tcpDump)
			if err != nil {
				fmt.Println("Unable to open log file", err)
				return
			}

			scanner := bufio.NewScanner(f)
			for {
				for scanner.Scan() {
					if strings.Contains(scanner.Text(), "IP") {
						ip, err := ParseIPFromTcpDump(scanner.Text())
						if err != nil {
							fmt.Println(err)
							return
						}
						fmt.Println("Request came from: ", ip)

						if cacheRes, ok := ipCache[ip]; ok {
							cacheRes.Hits += 1
							fmt.Printf(
								"Details of the IP:\n %+v \n",
								cacheRes)

							_, err := mongodb.MongoIPCollection.UpdateOne(
								context.TODO(),
								bson.D{
									{"query", cacheRes.Query},
								},
								bson.D{
									{
										"$inc", bson.D{
											{"hits", 1},
										},
									},
								},
							)
							if err != nil {
								fmt.Println("Could not update: ", err)
								continue
							}
							continue
						}
						response, err := getIPInfo(ip)
						if err != nil {
							fmt.Println(err)
							return
						}
						ipCache[ip] = &response

						fmt.Printf(
							"Details of the IP:\n %+v \n",
							response)

						if !persist || response.Status == "fail" {
							fmt.Println("Can't store to db")
							continue
						}

						updateRes, err := mongodb.MongoIPCollection.UpdateOne(
							context.TODO(),
							bson.D{
								{"query", response.Query},
							},
							bson.D{
								{
									"$inc", bson.D{
										{"hits", 1},
									},
								},
							},
						)
						if err != nil {
							fmt.Println("Could not update: ", err)
							continue
						}

						fmt.Println("Updated: ", updateRes.UpsertedCount)
						if updateRes.UpsertedCount > 0 {
							fmt.Println("Updated to mongo")
							continue

						}
						response.Hits = 1
						if _, err := mongodb.MongoIPCollection.InsertOne(
							context.TODO(),
							response,
						); err != nil {
							fmt.Println("error in inserting to mongo: ", err)
						} else {
							fmt.Println("Inserted to mongo")
						}

					}
				}
				if err := scanner.Err(); err != nil {
					fmt.Println(err)
					return
				}
			}
			return
		}

		// plain IP passing
		response, err := getIPInfo(ipAdd)
		if err != nil || response.Status == "fail" {
			//fmt.Println(err)
			return
		}
		fmt.Printf("Details of the IP:\n %+v \n", response)
		//if insertRes, err := mongodb.MongoIPCollection.InsertOne(
		//	context.TODO(),
		//	response,
		//); err != nil {
		//	fmt.Println("error in inserting to mongo: ", err)
		//} else {
		//	fmt.Println("Insert ID: ", insertRes.InsertedID)
		//}
		if !persist || response.Status == "fail" {
			return
		}

		updateRes, err := mongodb.MongoIPCollection.UpdateOne(
			context.TODO(),
			bson.D{
				{"query", response.Query},
			},
			bson.D{
				{
					"$inc", bson.D{
						{"hits", 1},
					},
				},
			},
		)
		if err != nil {
			fmt.Println("Could not update: ", err)
			return
		}
		fmt.Println("Updated: ", updateRes.UpsertedCount)
		if updateRes.UpsertedCount > 0 {
			fmt.Println("Updated to mongo")
			return

		}

		response.Hits = 1
		if _, err := mongodb.MongoIPCollection.InsertOne(
			context.TODO(),
			response,
		); err != nil {
			fmt.Println("error in inserting to mongo: ", err)
		} else {
			fmt.Println("Inserted to mongo")
		}

	},
}

func getIPInfo(ip string) (Response, error) {
	var response Response
	resp, err := http.Get(fmt.Sprintf("http://ip-api.com/json/%s", ip))
	if err != nil {
		fmt.Println(err)
		return response, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
		return response, err
	}
	err = json.Unmarshal(body, &response)
	if err != nil {
		fmt.Println("error in unmarshalling response: ", err)
		return response, err
	}
	return response, err
}

func ParseIPFromTcpDump(tcpDump string) (string, error) {
	pos := strings.LastIndex(tcpDump, ".")
	if pos == 0 || pos >= len(tcpDump) {
		return "", fmt.Errorf("pos of last . is out of range: %d", pos)
	}
	if net.ParseIP(tcpDump[:pos]) == nil {
		// it is not an IP, ignore
		return "", nil
	}
	return tcpDump[:pos], nil
	// NOTE: THis is the old logic for old command- sudo tcpdump -s 0 -A 'tcp[((tcp[12:1] & 0xf0) >> 2):4] = 0x47455420'

	//split := strings.Split(tcpDump, "\n")
	//if len(split) == 0 {
	//	return "", fmt.Errorf("error in parsing tcpdump output: %s", "split")
	//}
	//newSplit := strings.SplitAfter(split[0], ">")[0]
	//sliceSplit := strings.Fields(newSplit)
	//if len(sliceSplit) < 3 {
	//	return "", fmt.Errorf("error in parsing tcpdump output: %s", "sliceSplit")
	//}
	//lastDot := strings.LastIndex(sliceSplit[2], ".")
	//if lastDot == -1 {
	//	return "", fmt.Errorf("error in parsing tcpdump output: %s", "lastDot")
	//}
	//if len(sliceSplit[2]) < lastDot {
	//	return "", fmt.Errorf("error in parsing tcpdump output: %s", "lastDot length")
	//}
	//return sliceSplit[2][:lastDot], nil
}
