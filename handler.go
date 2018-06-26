package solver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	brainID      string
	roomID       string
	storedAnsPos int
	selfScore    int
	oppoScore    int
	randClicked  bool     // For click random answer
	luckyPedias  []string // For input question comment
	answers      []string // For input question comment

	prevQuizNum int // For getting random question from db for live streaming
)

func handleQuestionResp(bs []byte) {
	question := &Question{}
	storedAnsPos = 0
	if len(bs) > 0 && !strings.Contains(string(bs), "encryptedData") {
		// Get quiz from MITM
		json.Unmarshal(bs, question)

		// Get self and oppo score
		re := regexp.MustCompile(`"score":{"(\d+)":(\d+),"(\d+)":(\d+)}`)
		scores := re.FindStringSubmatch(string(bs))

		if len(scores) == 5 {
			if scores[1] == brainID {
				selfScore, _ = strconv.Atoi(scores[2])
				oppoScore, _ = strconv.Atoi(scores[4])
			} else if scores[3] == brainID {
				selfScore, _ = strconv.Atoi(scores[4])
				oppoScore, _ = strconv.Atoi(scores[2])
			}
		} else {
			selfScore, oppoScore = 0, 0
		}
	} else {
		// Get quiz from OCR
		question.Data.Quiz, question.Data.Options = getQuizFromOCR()
		if len(question.Data.Options) == 0 || question.Data.Quiz == "" {
			log.Println("No quiz or options found in screenshot...")
			return
		}
		quiz := question.Data.Quiz
		quiz = strings.Replace(quiz, "?", "？", -1)
		quiz = strings.Replace(quiz, ",", "，", -1)
		quiz = strings.Replace(quiz, "(", "（", -1)
		quiz = strings.Replace(quiz, ")", "）", -1)
		quiz = strings.Replace(quiz, "\"", "“", -1)
		quiz = strings.Replace(quiz, "'", "‘", -1)
		quiz = strings.Replace(quiz, "!", "！", -1)
		question.Data.Quiz = quiz
	}

	question.CalData.RoomID = roomID
	question.CalData.quizNum = strconv.Itoa(question.Data.Num)

	//Get the answer from the db if question fetched by MITM
	answer := FetchQuestion(question)

	// // fetch image of the quiz
	// keywords, quoted := preProcessQuiz(question.Data.Quiz, false)
	// imgTimeChan := make(chan int64)
	// go fetchAnswerImage(answer, keywords, quoted, imgTimeChan)

	go SetQuestion(question)

	answerItem := "不知道"
	ansPos := 0
	odds := make([]float32, len(question.Data.Options))
	if question.Data.Num == 1 {
		luckyPedias = make([]string, 0)
		answers = make([]string, 0)
	}

	if answer != "" {
		for i, option := range question.Data.Options {
			if option == answer {
				// question.Data.Options[i] = option + "[.]"
				ansPos = i + 1
				answerItem = option
				odds[i] = 888
				break
			}
		}
	}
	storedAnsPos = ansPos

	// Put true here to force searching, even if found answer in db
	if storedAnsPos == 0 {
		var ret map[string]int
		ret, luckyStr := GetFromAPI(question.Data.Quiz, question.Data.Options)

		luckyPedias = append(luckyPedias, luckyStr)

		log.Printf("Google predict => %v\n", ret)
		total := 1

		for _, option := range question.Data.Options {
			total += ret[option]
		}
		if total != 1 {
			// total == 1 -> 0,0,0,0
			max := math.MinInt32
			for i, option := range question.Data.Options {
				odds[i] = float32(ret[option]) / float32(total-ret[option])
				// question.Data.Options[i] = option + "[" + strconv.Itoa(ret[option]) + "]"
				if ret[option] > max && ret[option] != 0 {
					max = ret[option]
					ansPos = i + 1
					answerItem = option
				}
			}
		}
		// verify the stored answer
		if answer == answerItem {
			//good
			odds[ansPos-1] = 888
		} else {
			if answer != "" {
				// searched result could be wrong
				if storedAnsPos != 0 {
					re := regexp.MustCompile("\\p{Han}+")
					if odds[ansPos-1] < 5 || len(answer) > 6 || !re.MatchString(answer) {
						log.Println("searched answer could be wrong...")
						answerItem = answer
						ansPos = storedAnsPos
						odds[ansPos-1] = 333
					} else {
						// stored answer may be corrupted
						log.Println("stored answer may be corrupted...")
						odds[ansPos-1] = 444
					}
				} else {
					// if storedAnsPos==0, the stored anser exists, but match nothing => the option words changed by the game
					log.Println("the previous option words changed by the game...")
				}
			} else {
				log.Println("new question got!")
			}
			if len(odds) == 4 {
				storedAnsPos = ansPos
			}
		}
	}

	answers = append(answers, answerItem)
	if Mode == 1 && strings.Contains(string(bs), brainID) {
		go clickProcess(ansPos, question)
	} // click answer

	fmt.Printf(" 【Q】 %v\n 【A】 %v\n", question.Data.Quiz, answerItem)
	question.CalData.Answer = answerItem
	question.CalData.AnswerPos = ansPos
	question.CalData.Odds = odds
	questionInfo, _ = json.Marshal(question)

	// // Image time and question core information may not be sent in ONE http GET response to client
	// question.CalData.ImageTime = <-imgTimeChan
	// questionInfo, _ = json.Marshal(question)
	question = nil
}

func handleChooseResponse(bs []byte) {
	chooseResp := &ChooseResp{}
	json.Unmarshal(bs, chooseResp)

	//log.Println("response choose", roomID, chooseResp.Data.Num, string(bs))
	question := GetQuestion(roomID, strconv.Itoa(chooseResp.Data.Num))
	if question == nil {
		log.Println("error getting question", chooseResp.Data.RoomID, chooseResp.Data.Num)
		return
	}
	//If the question fetched by MITM, save it; elif fetched by OCR(no roomID or Num), don't save
	question.CalData.TrueAnswer = question.Data.Options[chooseResp.Data.Answer-1]
	if chooseResp.Data.Yes {
		question.CalData.TrueAnswer = question.Data.Options[chooseResp.Data.Option-1]
	}
	log.Printf("[SaveData]  %s -> %s\n\n", question.Data.Quiz, question.CalData.TrueAnswer)
	StoreQuestion(question)
	StoreWholeQuestion(question)
}

func handleNextQuestion(topic string) {
	question := &Question{}
	answers = make([]string, 0)

	// Get random question from db for live streaming
	question = FetchRandomQuestion(topic)
	ansPos := 0
	odds := make([]float32, len(question.Data.Options))
	for {
		question.Data.Num = rand.Intn(9) + 1
		if question.Data.Num != prevQuizNum {
			prevQuizNum = question.Data.Num
			break
		}
	}
	question.CalData.Odds = odds
	question.CalData.AnswerPos = ansPos
	questionInfo, _ = json.Marshal(question)

	answer := question.CalData.Answer

	// fetch image of the quiz
	keywords, quoted := preProcessQuiz(question.Data.Quiz, false)
	imgTimeChan := make(chan int64)
	go fetchAnswerImage(answer, keywords, quoted, imgTimeChan)

	// Image time and question core information may not be sent in ONE http GET response to client
	question.CalData.ImageTime = <-imgTimeChan
	questionInfo, _ = json.Marshal(question)
	question = nil
}

func handleCurrentAnswer(qNum int, user string, choice string) {
	question := &Question{}
	err := json.Unmarshal(questionInfo, question)
	if err != nil {
		log.Println(err.Error())
		return
	} else if question.Data.Num != qNum {
		log.Println("Question #id does not match current.")
		return
	} else if len(answers) > 0 {
		log.Println("Question has been answerd")
		return
	}

	if choice == "A" {
		question.CalData.Choice = 1
	} else if choice == "B" {
		question.CalData.Choice = 2
	} else if choice == "C" {
		question.CalData.Choice = 3
	} else if choice == "D" {
		question.CalData.Choice = 4
	} else {
		question.CalData.Choice = 0
	}
	answer := question.CalData.Answer
	ansPos := 0
	odds := make([]float32, len(question.Data.Options))
	for i, option := range question.Data.Options {
		if option == answer {
			ansPos = i + 1
			if ansPos == question.CalData.Choice {
				odds[i] = 666
				go recordCorrectUser(user)
			} else {
				odds[i] = 333
			}
			break
		}
	}
	question.CalData.Odds = odds
	question.CalData.AnswerPos = ansPos
	question.CalData.User = user
	questionInfo, _ = json.Marshal(question)
	question = nil

	answers = append(answers, answer)

	time.Sleep(10 * time.Second)
	handleNextQuestion("")
}

func clickProcess(ansPos int, question *Question) {
	var centerX = 540    // center of screen
	var firstItemY = 840 // center of first item (y)
	var optionHeight = 200
	var nextMatchY = 1650
	if ansPos >= 0 {
		// if ansPos == 0 || (!randClicked && question.Data.Num != 5 && (question.Data.Type == "时尚" || question.Data.Type == "电视" || question.Data.Type == "经济" || question.Data.Type == "日常")) {
		// 	// click randomly, only do it once on first 4 quiz
		// 	ansPos = rand.Intn(4) + 1
		// 	randClicked = true
		// }
		if ansPos == 0 || selfScore-oppoScore > 500 || (question.Data.Num < 5 && selfScore-oppoScore > 250) {
			// click randomly, only do it when have big advantage
			correctAnsPos := ansPos
			for {
				ansPos = rand.Intn(4) + 1
				if ansPos != correctAnsPos {
					break
				}
			}
			randClicked = true
		}
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(300)+3000))
		go clickAction(centerX, firstItemY+optionHeight*(ansPos-1))
		time.Sleep(time.Millisecond * 1000)
		go clickAction(centerX, firstItemY+optionHeight*(ansPos-1))
		time.Sleep(time.Millisecond * 500)
		go clickAction(centerX, firstItemY+optionHeight*(4-1))
		if rand.Intn(100) < 7 {
			time.Sleep(time.Millisecond * 500)
			go clickEmoji()
		}
	} else {
		// go to next match
		randClicked = false
		selfScore = 0
		oppoScore = 0

		// inputADBText()

		time.Sleep(time.Millisecond * 500)
		go swipeAction() // go back to game selection menu
		time.Sleep(time.Millisecond * 800)
		go clickAction(centerX, nextMatchY) // start new game
		time.Sleep(time.Millisecond * 1000)
		go clickAction(centerX, nextMatchY)
	}
}

func clickAction(posX int, posY int) {
	touchX, touchY := strconv.Itoa(posX+rand.Intn(400)-200), strconv.Itoa(posY+rand.Intn(50)-25)
	_, err := exec.Command("adb", "shell", "input", "tap", touchX, touchY).Output()
	if err != nil {
		log.Println("error: check adb connection.", err)
	}
}

func swipeAction() {
	_, err := exec.Command("adb", "shell", "input", "swipe", "75", "150", "75", "150", "0").Output() // swipe right, back
	if err != nil {
		log.Println("error: check adb connection.", err)
	}
}

func clickEmoji() {
	_, err := exec.Command("adb", "shell", "input", "tap", "100", "300").Output() // tap my avatar to summon emoji panel
	if err != nil {
		log.Println("error: check adb connection.", err)
	}
	time.Sleep(time.Millisecond * 100)
	fX, fY := 170, 560
	dX, dY := 150, 150
	touchX, touchY := strconv.Itoa(fX+dX*(rand.Intn(2)*3+rand.Intn(2))), strconv.Itoa(fY+dY*rand.Intn(3))
	_, err = exec.Command("adb", "shell", "input", "tap", touchX, touchY).Output() // tap the emoji
	if err != nil {
		log.Println("error: check adb connection.", err)
	}
}

func inputADBText() {
	search := make(chan string, 5)
	done := make(chan bool, 1)
	count := cap(search)
	for i := 0; i < len(answers); i++ {
		// donot search the pure number answer, meaningless
		reNum := regexp.MustCompile("[0-9]+")
		if !reNum.MatchString(answers[i]) {
			go searchBaiduBaike(answers, i+1, search)
		}
	}
	go func() {
		for {
			s, more := <-search
			if more {
				// The first 8 chars in text is the identifier of the search source, 4th is the index
				id := s[:8]
				idx, _ := strconv.Atoi(s[4:5])
				log.Println("search received...", id, idx)
				if idx <= len(luckyPedias) && len(luckyPedias[idx-1]) < 60 {
					luckyPedias[idx-1] = s[8:]
				}
				count--
				if count == 0 {
					done <- true
					return
				}
			}
		}
	}()

	time.Sleep(time.Millisecond * 500)
	exec.Command("adb", "shell", "input", "tap", "1000", "1050").Output() // tap `review current game`
	time.Sleep(time.Millisecond * 4000)

	select {
	case <-done:
		fmt.Println("search done")
	case <-time.After(2 * time.Second):
		fmt.Println("search timeout")
	}

	for index := 0; index < len(luckyPedias); index++ {
		exec.Command("adb", "shell", "input", "tap", "500", "1700").Output() // tap `input bar`
		time.Sleep(time.Millisecond * 200)
		re := regexp.MustCompile("[\\n\"]+")
		quoted := regexp.MustCompile("\\[[^\\]]+\\]")
		msg := re.ReplaceAllString(luckyPedias[index], "")
		msg = quoted.ReplaceAllString(msg, "")
		if len([]rune(msg)) > 500 {
			msg = string([]rune(msg)[:500])
		}
		println(msg)
		exec.Command("adb", "shell", "am", "broadcast", "-a ADB_INPUT_TEXT", "--es msg", "\""+msg+"\"").Output() // sending text input
		time.Sleep(time.Millisecond * 300)
		exec.Command("adb", "shell", "am", "broadcast", "-a ADB_EDITOR_CODE", "--ei code", "4").Output() // editor action `send`
		time.Sleep(time.Millisecond * 200)
		exec.Command("adb", "shell", "input", "swipe", "800", "470", "200", "470", "200").Output() // swipe left, forward
		time.Sleep(time.Millisecond * 300)
	}
	exec.Command("adb", "shell", "input", "tap", "500", "500").Output() // tap center, esc dialog box, to go back
	exec.Command("adb", "shell", "input", "tap", "75", "150").Output()  // tap esc arrow, go back
}

func recordCorrectUser(user string) {
	ranking := make(map[string]int)
	bs, _ := ioutil.ReadFile("ranking.txt")
	txt := string(bs)
	lines := strings.Split(txt, "\n")
	for _, line := range lines {
		if len(strings.Split(line, "\t")) == 2 {
			name := strings.Split(line, "\t")[1]
			count, _ := strconv.Atoi(strings.Split(line, "\t")[0])
			ranking[name] = count
			log.Println(name, count)
		}
	}

	if val, ok := ranking[user]; ok {
		ranking[user] = val + 1
	} else {
		ranking[user] = 1
	}

	type kv struct {
		Key   string
		Value int
	}

	var ss []kv
	for k, v := range ranking {
		ss = append(ss, kv{k, v})
	}

	sort.Slice(ss, func(i, j int) bool {
		return ss[i].Value > ss[j].Value
	})

	contents := ""
	for _, kv := range ss {
		contents += fmt.Sprintf("%d\t%s\n", kv.Value, kv.Key)
	}
	contents += "|\n|\n|\n"
	ioutil.WriteFile("ranking.txt", []byte(contents), 0644)
}

type Question struct {
	Data struct {
		Quiz        string   `json:"quiz"`
		Options     []string `json:"options"`
		Num         int      `json:"num"`
		School      string   `json:"school"`
		Type        string   `json:"type"`
		Contributor string   `json:"contributor"`
		EndTime     int      `json:"endTime"`
		CurTime     int      `json:"curTime"`
	} `json:"data"`
	CalData struct {
		RoomID     string
		quizNum    string
		Answer     string
		AnswerPos  int
		TrueAnswer string
		Odds       []float32
		ImageTime  int64
		User       string
		Choice     int
		Voice      int
	} `json:"caldata"`
	Errcode int `json:"errcode"`
}

type ChooseResp struct {
	Data struct {
		UID         int  `json:"uid"`
		Num         int  `json:"num"`
		Answer      int  `json:"answer"`
		Option      int  `json:"option"`
		Yes         bool `json:"yes"`
		Score       int  `json:"score"`
		TotalScore  int  `json:"totalScore"`
		RowNum      int  `json:"rowNum"`
		RowMult     int  `json:"rowMult"`
		CostTime    int  `json:"costTime"`
		RoomID      int  `json:"roomId"`
		EnemyScore  int  `json:"enemyScore"`
		EnemyAnswer int  `json:"enemyAnswer"`
	} `json:"data"`
	Errcode int `json:"errcode"`
}

//roomID=476376430&quizNum=4&option=4&uid=26394007&t=1515326786076&sign=3592b9d28d045f3465206b4147ea872b
